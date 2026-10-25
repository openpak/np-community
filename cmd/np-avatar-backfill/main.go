// Backfill missing core avatars from the website's canonical public images.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/url"
	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"openpak/np-common/coreclient"
	"openpak/np-community/internal/config"
	"os"
	"time"
)

func main() {
	source := flag.String("source", "https://openpak.org", "canonical website origin")
	flag.Parse()
	origin, err := url.Parse(*source)
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil {
		log.Fatal("HTTPS website origin required")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	c, err := coreclient.Dial(cfg.CoreAddress, cfg.CoreKey)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Host != origin.Host || r.URL.Scheme != "https" {
			return errors.New("cross-origin redirect")
		}
		if len(via) > 3 {
			return errors.New("redirect limit")
		}
		return nil
	}}
	count := 0
	for after := ""; ; {
		accounts, next, err := c.AccountPage(ctx, after)
		if err != nil {
			log.Fatal(err)
		}
		for _, a := range accounts {
			if _, err := c.Avatar(ctx, a.ID, ""); err == nil {
				continue
			} else if !errors.Is(err, coreclient.ErrNotFound) {
				log.Fatal(err)
			}
			images := map[int][]byte{}
			for _, size := range []int{256, 512} {
				u := origin.JoinPath("media", "avatars", a.ID, fmt.Sprint(size))
				req, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
				resp, err := client.Do(req)
				if err != nil {
					log.Fatal(err)
				}
				data, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
				resp.Body.Close()
				if err != nil || resp.StatusCode != 200 || len(data) > 1<<20 {
					log.Fatal("website avatar fetch failed")
				}
				image, err := png.DecodeConfig(bytes.NewReader(data))
				if err != nil || image.Width != size || image.Height != size {
					log.Fatal("invalid website PNG")
				}
				images[size] = data
			}
			// Recheck before writing so an avatar uploaded during fetching is preserved.
			if _, err := c.Avatar(ctx, a.ID, ""); err == nil {
				continue
			} else if !errors.Is(err, coreclient.ErrNotFound) {
				log.Fatal(err)
			}
			if err := c.PutAvatar(ctx, &accountv1.PutAvatarRequest{AccountId: a.ID, Png_256: images[256], Png_512: images[512]}); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Avatar synchronized:", a.DisplayName)
			count++
		}
		if next == "" {
			break
		}
		if next <= after {
			log.Fatal("invalid directory cursor")
		}
		after = next
	}
	fmt.Printf("Missing avatars synchronized: %d\n", count)
}
