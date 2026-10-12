package workspace_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jyecusch/forkit/internal/apperr"
	"github.com/jyecusch/forkit/internal/gittest"
	"github.com/jyecusch/forkit/internal/model"
	"github.com/jyecusch/forkit/internal/repo"
	"github.com/jyecusch/forkit/internal/synchub"
	"github.com/jyecusch/forkit/internal/workspace"
)

type remoteFixture struct {
	t   *testing.T
	ws  *workspace.Workspace
	rec *recorder
	// git runs commands in any of the fixture's repositories.
	git    *gittest.Repo
	local  string
	origin string
	// other is a second clone, standing in for a teammate pushing to origin.
	other  string
	repo   model.Repo
	mainWT model.WorktreeID
}

// newRemoteFixture opens a clone of a bare origin whose main has one commit.
func newRemoteFixture(t *testing.T) *remoteFixture {
	t.Helper()
	seed := gittest.New(t)
	seed.Commit("initial")
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	local := filepath.Join(base, "local")
	other := filepath.Join(base, "other")
	seed.Git("clone", "-q", "--bare", seed.Dir, origin)
	seed.Git("clone", "-q", origin, local)
	seed.Git("clone", "-q", origin, other)

	ws, rec, _ := newWorkspace(t)
	opened, err := ws.Open(context.Background(), local)
	if err != nil {
		t.Fatal(err)
	}
	rec.waitFor(t, synchub.EventName("repos", workspace.ReposScope), func(v json.RawMessage) bool {
		return strings.Contains(string(v), `"state":"ready"`)
	})
	return &remoteFixture{
		t: t, ws: ws, rec: rec, git: seed, local: local, origin: origin, other: other,
		repo: opened, mainWT: repo.WorktreeIDFor(local),
	}
}

func (f *remoteFixture) in(dir string, args ...string) string {
	f.t.Helper()
	return f.git.GitIn(dir, args...)
}

func (f *remoteFixture) pushFromOther(message string) string {
	f.t.Helper()
	f.in(f.other, "commit", "--allow-empty", "-qm", message)
	f.in(f.other, "push", "-q", "origin", "main")
	return f.in(f.other, "rev-parse", "HEAD")
}

// waitForHead waits until the workspace, told by its file watcher, sees the
// main worktree on branch.
func (f *remoteFixture) waitForHead(branch string) {
	f.t.Helper()
	refID := string(repo.RefIDFor(f.repo.ID, "refs/heads/"+branch))
	scope := string(f.repo.ID)
	f.rec.waitFor(f.t, synchub.EventName("refs", scope), func(v json.RawMessage) bool {
		return strings.Contains(string(v), `"id":"`+refID+`"`)
	})
	f.rec.waitFor(f.t, synchub.EventName("worktrees", scope), func(v json.RawMessage) bool {
		return strings.Contains(string(v), `"refId":"`+refID+`"`)
	})
}

func TestFetchAddsAndPrunesRemoteBranches(t *testing.T) {
	f := newRemoteFixture(t)
	f.in(f.origin, "branch", "gone", "main")
	if err := f.ws.Fetch(context.Background(), f.repo.ID); err != nil {
		t.Fatal(err)
	}
	f.in(f.origin, "branch", "-D", "gone")
	f.in(f.origin, "branch", "added", "main")
	tip := f.pushFromOther("upstream work")

	if err := f.ws.Fetch(context.Background(), f.repo.ID); err != nil {
		t.Fatal(err)
	}
	refs := f.in(f.local, "for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	if !strings.Contains(refs, "refs/remotes/origin/added") || strings.Contains(refs, "refs/remotes/origin/gone") {
		t.Fatalf("remote branches after fetch:\n%s", refs)
	}
	if got := f.in(f.local, "rev-parse", "origin/main"); got != tip {
		t.Fatalf("origin/main at %s, want %s", got, tip)
	}
}

func TestFetchReportsAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="forkit"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	f := newRemoteFixture(t)
	f.in(f.local, "remote", "set-url", "origin", server.URL+"/repo.git")

	err := f.ws.Fetch(context.Background(), f.repo.ID)
	if got := apperr.ToRPC(err).Code; got != model.ErrorCodeAuthFailed {
		t.Fatalf("got %s (%v), want AuthFailed", got, err)
	}
}

func TestPullFastForwards(t *testing.T) {
	f := newRemoteFixture(t)
	tip := f.pushFromOther("upstream work")

	if err := f.ws.Pull(context.Background(), f.mainWT); err != nil {
		t.Fatal(err)
	}
	if got := f.in(f.local, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD at %s, want %s", got, tip)
	}
}

func TestPullRefusesDivergedBranch(t *testing.T) {
	f := newRemoteFixture(t)
	f.pushFromOther("upstream work")
	f.in(f.local, "commit", "--allow-empty", "-qm", "local work")
	before := f.in(f.local, "rev-parse", "HEAD")

	err := f.ws.Pull(context.Background(), f.mainWT)
	if codeOf(err) != model.ErrorCodeNotFastForward {
		t.Fatalf("got %v, want NotFastForward", err)
	}
	if got := f.in(f.local, "rev-parse", "HEAD"); got != before {
		t.Fatalf("HEAD moved to %s", got)
	}
}

func TestPullWithoutUpstream(t *testing.T) {
	f := newRemoteFixture(t)
	f.in(f.local, "checkout", "-qb", "local-only")
	f.waitForHead("local-only")

	if err := f.ws.Pull(context.Background(), f.mainWT); codeOf(err) != model.ErrorCodeNoUpstream {
		t.Fatalf("got %v, want NoUpstream", err)
	}
}

func TestPushPublishesNewBranchAndTracksIt(t *testing.T) {
	f := newRemoteFixture(t)
	f.in(f.local, "checkout", "-qb", "feature")
	f.in(f.local, "commit", "--allow-empty", "-qm", "feature work")
	f.waitForHead("feature")

	if err := f.ws.Push(context.Background(), f.mainWT); err != nil {
		t.Fatal(err)
	}
	if got, want := f.in(f.origin, "rev-parse", "feature"), f.in(f.local, "rev-parse", "HEAD"); got != want {
		t.Fatalf("origin feature at %s, want %s", got, want)
	}
	if got := f.in(f.local, "rev-parse", "--abbrev-ref", "feature@{upstream}"); got != "origin/feature" {
		t.Fatalf("upstream is %q", got)
	}
}

func TestPushRejectedWhenRemoteIsAhead(t *testing.T) {
	f := newRemoteFixture(t)
	upstream := f.pushFromOther("upstream work")
	f.in(f.local, "commit", "--allow-empty", "-qm", "local work")

	if err := f.ws.Push(context.Background(), f.mainWT); codeOf(err) != model.ErrorCodeNotFastForward {
		t.Fatalf("got %v, want NotFastForward", err)
	}
	if got := f.in(f.origin, "rev-parse", "main"); got != upstream {
		t.Fatalf("origin main moved to %s", got)
	}
}
