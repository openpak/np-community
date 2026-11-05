package community

import (
	"fmt"
	"net"
	"net/http"
	"openpak/np-common/resource"
	"regexp"
	"strconv"
	"strings"
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
	// vsh verifies this resource with its own key (receive mode 3), not the trophy key.
	key := s.MatchingKey
	if len(key) != 20 {
		key = s.ResourceKey
	}
	if len(key) != 20 {
		http.Error(w, "resource signing unavailable", http.StatusServiceUnavailable)
		return
	}
	xml := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><matching2 status="maintenance" titleid="%s"/>`, title))
	if host, port, ok := parseMatchingAgent(s.MatchingAgent); ok {
		xml = []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><matching2 status="alive" titleid="%s"><agent-fqdn id="1" status="alive" port="%d">%s</agent-fqdn></matching2>`, title, port, host))
	}
	body := resource.Sign(xml, key)
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(body)
}

// parseMatchingAgent splits NP_MATCHING_AGENT ("hostname:port"). Empty or malformed
// means no agent: the resource keeps reporting maintenance.
func parseMatchingAgent(v string) (host string, port int, ok bool) {
	h, p, err := net.SplitHostPort(v)
	if err != nil || h == "" || strings.ContainsAny(h, "<>&\"' ") {
		return "", 0, false
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 0 || n > 65535 {
		return "", 0, false
	}
	return h, n, true
}
