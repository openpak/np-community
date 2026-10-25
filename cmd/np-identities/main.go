// Command np-identities performs an idempotent NP identity backfill and inspects
// an existing user's canonical friend projection. It never takes passwords.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"openpak/np-common/coreclient"
	"openpak/np-community/community"
	"openpak/np-community/internal/config"
	"os"
	"strings"
	"time"
)

func main() {
	list := flag.Bool("list", false, "list active account display names and NP online IDs")
	migrate := flag.Bool("migrate", false, "provision NP identities for all active accounts")
	online := flag.String("online-id", "", "inspect this existing NP user's roster")
	ids := flag.String("account-ids", "", "optional newline-separated core account IDs from a home-only directory export")
	flag.Parse()
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		log.Fatal(e)
	}
	c, e := coreclient.Dial(cfg.CoreAddress, cfg.CoreKey)
	if e != nil {
		log.Fatal(e)
	}
	defer c.Close()
	s := &community.Service{Core: c}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if *list {
		after := ""
		count := 0
		for {
			accounts, next, e := c.AccountPage(ctx, after)
			if e != nil {
				log.Fatal(e)
			}
			for _, a := range accounts {
				l, e := c.GetLinkByAccount(ctx, a.ID)
				if e != nil {
					log.Fatal(e)
				}
				fmt.Printf("%s\t%s\n", a.DisplayName, l.OnlineID)
				count++
			}
			if next == "" {
				break
			}
			if next <= after {
				log.Fatal("directory cursor did not advance")
			}
			after = next
		}
		fmt.Printf("Active accounts: %d\n", count)
	}
	if *migrate {
		count := 0
		if *ids == "" {
			count, e = s.ProvisionAll(ctx, c)
		} else {
			f, err := os.Open(*ids)
			if err != nil {
				log.Fatal(err)
			}
			defer f.Close()
			scan := bufio.NewScanner(f)
			for scan.Scan() {
				id := strings.TrimSpace(scan.Text())
				if id == "" {
					continue
				}
				a, err := c.GetAccount(ctx, id)
				if err != nil {
					log.Fatal(err)
				}
				if _, err = s.EnsureLink(ctx, a); err != nil {
					log.Fatal(err)
				}
				count++
			}
			e = scan.Err()
		}
		if e != nil {
			log.Fatal(e)
		}
		fmt.Printf("NP identities reconciled: %d active accounts\n", count)
	}
	if *online != "" {
		l, e := c.GetLinkByOnlineID(ctx, *online)
		if e != nil {
			log.Fatal(e)
		}
		fs, e := s.Roster(ctx, l.AccountID)
		if e != nil {
			log.Fatal(e)
		}
		fmt.Printf("%s: %d OpenPak friends\n", *online, len(fs))
		for _, f := range fs {
			fmt.Printf("%s\t%s\n", f.OnlineID, f.DisplayName)
		}
	}
	if !*migrate && !*list && *online == "" {
		flag.Usage()
		os.Exit(2)
	}
}
