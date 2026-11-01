package community

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNSXFeeds(t *testing.T) {
	h := nsxHandler()
	const base = "/nsx/sec/Xz78TMQ1Uf31VCYr/c/NSXWSV/"
	for path, want := range map[string]int{
		base + "NSXWSV-PN.P3.PT-WHATSNEW00000001.xml":           200,
		base + "NSXWSV-PN.P3.US-WHATSNEW00000001.xml":           200,
		base + "NSXWSV-PN.P3.GAME.PT-BILLBOARD0000001.xml":      200,
		base + "NSXWSV-PN.P3.VIDEO.GB-BILLBOARD0000001.xml":     200,
		base + "NSXWSV-PN.P3.PT-XMB_TV_INSTALL01.xml":           404,
		"/nsx/sec/Xz78TMQ1Uf31VCYr/img/openpak-256.png":         200,
		"/nsx/sec/Xz78TMQ1Uf31VCYr/c/NSXWSV/../../../../go.mod": 404,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: %d, want %d", path, rec.Code, want)
		}
		if want == 200 && strings.HasSuffix(path, ".xml") && !strings.Contains(rec.Body.String(), "<mtrl ") {
			t.Errorf("%s: no mtrl item", path)
		}
	}
}
