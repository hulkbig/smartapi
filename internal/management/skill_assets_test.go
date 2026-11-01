package management

import (
	"ferry/internal/controlstore"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillDistributionRoutes(t *testing.T) {
	store, err := controlstore.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h := New(Deps{Security: store, Control: store}).Routes()
	for _, path := range []string{"/skills/smartapi/SKILL.md", "/skills/smartapi/INSTALL.md", "/skills/smartapi/smartapi.zip", "/skills/ferry/SKILL.md", "/skills/ferry/ferry.zip"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	for path, target := range map[string]string{"/SKILL.md": "/skills/smartapi/SKILL.md", "/skill.md": "/skills/smartapi/SKILL.md", "/install.md": "/skills/smartapi/INSTALL.md"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 307 || w.Header().Get("Location") != target {
			t.Errorf("alias %s: %d %s", path, w.Code, w.Header().Get("Location"))
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/skills/smartapi/SKILL.md", nil))
	protected := httptest.NewRecorder()
	h.ServeHTTP(protected, httptest.NewRequest("GET", "/api/access", nil))
	if protected.Code != 428 {
		t.Fatalf("protected API exposed: %d", protected.Code)
	}
	if !strings.Contains(w.Body.String(), "name: smartapi") {
		t.Fatal("not Skill markdown")
	}
}
