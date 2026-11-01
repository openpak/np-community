package community

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"regexp"
)

// nsxFiles is the XMB content feed tree served on nsx.sec.<env>.dl.playstation.net
// (What's New, Game/Video column billboards). Layout: docs/protocol.md "NSX feeds".
//
//go:embed nsx
var nsxFiles embed.FS

// The firmware puts the account country in every feed name (NSXWSV-PN.P3.GAME.PT-BILLBOARD0000001.xml).
// Items carry no country filter, so every country maps to one file (GAME-BILLBOARD.xml).
var nsxFeedName = regexp.MustCompile(`^NSXWSV-PN\.P3\.(?:(GAME|VIDEO)\.)?[A-Z]{2}-([A-Z_]+?)0*1\.xml$`)

func nsxHandler() http.Handler {
	root, _ := fs.Sub(nsxFiles, "nsx")
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir, name := path.Split(r.URL.Path)
		if m := nsxFeedName.FindStringSubmatch(name); m != nil {
			if m[1] != "" {
				m[2] = m[1] + "-" + m[2]
			}
			r.URL.Path = dir + m[2] + ".xml"
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		files.ServeHTTP(w, r)
	})
}
