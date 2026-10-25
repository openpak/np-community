package community

import (
	"context"
	"errors"
	"net/http"
	"openpak/np-common/coreclient"
	"strconv"
	"strings"
)

type Media interface {
	Avatar(context.Context, string, string) (*coreclient.Avatar, error)
}

const defaultAvatarBase = "http://avatar.np.community.playstation.net"

// AvatarURL is empty when there is no uploaded avatar; existing XMB defaults stay
// in use. The core owns the validated 256px PNG and its content version.
func (s *Service) AvatarURL(ctx context.Context, id string) string {
	media, ok := s.Core.(Media)
	if !ok {
		return ""
	}
	a, e := media.Avatar(ctx, id, "")
	if e != nil || len(a.PNG) == 0 {
		return ""
	}
	base := s.AvatarBaseURL
	if base == "" {
		base = defaultAvatarBase
	}
	cap := s.Tickets.AvatarCapability(id)
	if cap == "" {
		return ""
	}
	url := strings.TrimRight(base, "/") + "/a/" + cap + ".png"
	if len(url) > 127 {
		return ""
	}
	return url
}
func (s *Service) avatar(w http.ResponseWriter, r *http.Request) {
	cap := strings.TrimSuffix(r.PathValue("cap"), ".png")
	id, e := s.Tickets.VerifyAvatar(cap)
	if e != nil {
		http.NotFound(w, r)
		return
	}
	if _, e = s.Core.GetAccount(r.Context(), id); e != nil {
		http.NotFound(w, r)
		return
	}
	media, ok := s.Core.(Media)
	if !ok {
		http.NotFound(w, r)
		return
	}
	etag := strings.Trim(r.Header.Get("If-None-Match"), `"`)
	a, e := media.Avatar(r.Context(), id, etag)
	if e != nil {
		if errors.Is(e, coreclient.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "avatar unavailable", 503)
		}
		return
	}
	w.Header().Set("ETag", `"`+a.Version+`"`)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if a.NotModified {
		w.WriteHeader(304)
		return
	}
	if len(a.PNG) > 256<<10 {
		http.Error(w, "avatar exceeds limit", 502)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Length", strconv.Itoa(len(a.PNG)))
	w.Write(a.PNG)
}
