package community

import (
	"fmt"
	"net/http"
	"openpak/np-common/resource"
	"regexp"
	"strconv"
)

var matchingTitlePattern = regexp.MustCompile(`^[A-Z]{4}[0-9]{5}_[0-9]{2}$`)

// matchingResource is the native VSH discovery resource, not an RPCN endpoint.
// VSH's URL builder uses the communication ID in both directory and filename.
// Until a native Matching2 agent is implemented, report maintenance explicitly;
// advertising the private RPCN listener here would give real consoles a server
// with an incompatible protocol.
func (s *Service) matchingResource(w http.ResponseWriter, r *http.Request) {
	title := r.PathValue("title")
	if !matchingTitlePattern.MatchString(title) || r.PathValue("resource") != title+"-matching.xml" {
		http.NotFound(w, r)
		return
	}
	if len(s.ResourceKey) != 20 {
		http.Error(w, "resource signing unavailable", http.StatusServiceUnavailable)
		return
	}
	xml := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><matching2 status="maintenance" titleid="%s"/>`, title))
	body := resource.Sign(xml, s.ResourceKey)
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}
