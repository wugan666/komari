package public

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gin-gonic/gin"
)

// Agent install packages served by the controller itself, so new installs and
// their checksum verification never depend on GitHub. The files are baked into
// the controller image (or placed under data/agent-dist) by the release build.

var agentDistFile = regexp.MustCompile(`^(install\.(sh|ps1)|VERSION|checksums\.txt|komari-agent-[a-z0-9]+-[a-z0-9]+(\.exe)?(\.sha256)?)$`)

func agentDistDir() string {
	if dir := os.Getenv("KOMARI_AGENT_DIST"); dir != "" {
		return dir
	}
	if _, err := os.Stat("./data/agent-dist/VERSION"); err == nil {
		return "./data/agent-dist"
	}
	return "/app/agent-dist"
}

// ServeAgentDist serves one whitelisted agent install file by name.
func ServeAgentDist(c *gin.Context) {
	name := c.Param("file")
	if name == "" || !agentDistFile.MatchString(name) {
		c.Status(http.StatusNotFound)
		return
	}
	path := filepath.Join(agentDistDir(), name)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	switch {
	case name == "install.sh":
		c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	case name == "install.ps1":
		c.Header("Content-Type", "text/plain; charset=utf-8")
	case name == "VERSION" || name == "checksums.txt" || filepath.Ext(name) == ".sha256":
		c.Header("Content-Type", "text/plain; charset=utf-8")
	default:
		c.Header("Content-Type", "application/octet-stream")
	}
	// Every file changes with a release and a stale binary would fail its
	// checksum, so caches must revalidate.
	c.Header("Cache-Control", "no-cache")
	c.File(path)
}
