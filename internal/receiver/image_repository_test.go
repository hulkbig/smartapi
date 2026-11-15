package receiver

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundImageRepositoryArchiveInspectAndEnvironment(t *testing.T) {
	if ImageRepository != "ferry" {
		t.Fatal("legacy default changed")
	}
	original := ImageRepository
	t.Cleanup(func() { ImageRepository = original })
	for _, repository := range []string{"ferry", "smartapi"} {
		t.Run(repository, func(t *testing.T) {
			ImageRepository = repository
			root := t.TempDir()
			imagePath := filepath.Join(root, "image.tar.gz")
			for _, tag := range []string{repository + ":" + testSHA, "other:" + testSHA, repository + ":latest", repository + ":" + strings.Repeat("b", 40)} {
				raw := bundleBytes(t, map[string]string{"manifest.json": fmt.Sprintf(`[{"RepoTags":[%q]}]`, tag)})
				if err := os.WriteFile(imagePath, raw, 0600); err != nil {
					t.Fatal(err)
				}
				err := verifyDockerArchive(imagePath, testSHA)
				if (err == nil) != (tag == repository+":"+testSHA) {
					t.Fatalf("tag %s: %v", tag, err)
				}
			}
			for _, fixture := range []struct {
				revision, architecture string
				valid                  bool
			}{
				{testSHA, "amd64", true}, {strings.Repeat("b", 40), "amd64", false}, {testSHA, "arm64", false},
			} {
				calls := 0
				env := &Environment{run: func(_ context.Context, _ io.Reader, command string, args ...string) ([]byte, error) {
					calls++
					if command != dockerPath || len(args) != 5 || args[0] != "image" || args[1] != "inspect" || args[2] != repository+":"+testSHA || args[3] != "--format" {
						t.Fatalf("wrong inspect authority: %s %v", command, args)
					}
					if strings.Contains(args[4], "revision") {
						return []byte(fixture.revision), nil
					}
					if args[4] != "{{.Architecture}}" {
						t.Fatalf("unexpected inspection %v", args)
					}
					return []byte(fixture.architecture), nil
				}}
				if err := env.verifyImage(context.Background(), testSHA); (err == nil) != fixture.valid {
					t.Fatalf("inspect fixture %+v: %v", fixture, err)
				}
				if fixture.valid && calls != 2 {
					t.Fatal("missing revision/architecture check")
				}
			}
			stage := filepath.Join(root, "receiver-state", "staging", testOp)
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			bundle := bundleBytes(t, map[string]string{"compose.yml": "services: {}\n", "worker-README.md": "test", "bootstrap-config.template.yaml": "server: {}\n"})
			if err := os.WriteFile(filepath.Join(stage, "release-bundle.tar.gz"), bundle, 0600); err != nil {
				t.Fatal(err)
			}
			env := &Environment{ServiceDir: root}
			state := &operationState{Schema: "ferry/receiver-operation/v1", OperationID: testOp, ReleaseSHA: testSHA, Phase: "image-loaded", BundleDigest: fmt.Sprintf("%x", sha256.Sum256(bundle))}
			if err := env.saveOperation(state); err != nil {
				t.Fatal(err)
			}
			if _, err := env.installFiles(Request{OperationID: testOp, ReleaseSHA: testSHA}); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "releases", testSHA, ".env")
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != "SERVICE_IMAGE="+repository+":"+testSHA+"\n" {
				t.Fatalf("environment %q: %v", raw, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("environment must remain private")
			}
		})
	}
}
