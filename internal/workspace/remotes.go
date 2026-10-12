package workspace

import (
	"context"
	"fmt"
	"time"

	"github.com/jyecusch/forkit/internal/apperr"
	"github.com/jyecusch/forkit/internal/gitexec"
	"github.com/jyecusch/forkit/internal/model"
	"github.com/jyecusch/forkit/internal/repo"
	"github.com/jyecusch/forkit/internal/watch"
)

// networkTimeout bounds fetches, pulls and pushes, which can take far longer
// than gitexec.DefaultTimeout on large repositories or slow links.
const networkTimeout = 10 * time.Minute

// Fetch updates every remote's branches and tags, pruning branches deleted
// on the remote.
func (w *Workspace) Fetch(ctx context.Context, repoID model.RepoID) error {
	r, err := w.repo(repoID)
	if err != nil {
		return err
	}
	err = r.runOperation(ctx, model.OperationKindFetch, "", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, networkTimeout)
		defer cancel()
		_, err := w.git.Repo(r.loc.CommonDir, r.loc.WorkDir).Mutate(ctx, "fetch", "--all", "--prune")
		return err
	})
	if err != nil {
		return err
	}
	return r.refresh(ctx, watch.Change{Refs: true})
}

// Pull fast-forwards a worktree's branch to its upstream. A branch that has
// diverged is refused rather than merged or rebased.
func (w *Workspace) Pull(ctx context.Context, worktreeID model.WorktreeID) error {
	r, wt, branch, err := w.branchToSync(worktreeID, "pull")
	if err != nil {
		return err
	}
	if branch.UpstreamRefID == "" {
		return apperr.New(model.ErrorCodeNoUpstream, fmt.Sprintf("%s does not track a remote branch, so there is nothing to pull.", branch.ShortName))
	}
	err = r.runOperation(ctx, model.OperationKindPull, wt.ID, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, networkTimeout)
		defer cancel()
		_, err := w.git.Repo(r.loc.CommonDir, wt.Path).Mutate(ctx, "-c", "pull.rebase=false", "pull", "--ff-only")
		if gitexec.IsNotFastForward(err) {
			return apperr.New(model.ErrorCodeNotFastForward,
				fmt.Sprintf("%s and its upstream have both changed, so it cannot be fast-forwarded.", branch.ShortName))
		}
		return err
	})
	if refreshErr := r.refresh(ctx, watch.Change{Refs: true, Worktrees: true, Status: map[model.WorktreeID]bool{wt.ID: true}}); err == nil {
		err = refreshErr
	}
	return err
}

// Push sends a worktree's branch to its upstream. A branch without one is
// pushed to origin, or the only remote, and set to track what it created
// there.
func (w *Workspace) Push(ctx context.Context, worktreeID model.WorktreeID) error {
	r, wt, branch, err := w.branchToSync(worktreeID, "push")
	if err != nil {
		return err
	}
	args := []string{"push"}
	if branch.UpstreamRefID == "" {
		remote, err := r.defaultPushRemote(branch)
		if err != nil {
			return err
		}
		args = []string{"push", "--set-upstream", remote, branch.FullName}
	}
	err = r.runOperation(ctx, model.OperationKindPush, wt.ID, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, networkTimeout)
		defer cancel()
		_, err := w.git.Repo(r.loc.CommonDir, wt.Path).Mutate(ctx, args...)
		if gitexec.IsNotFastForward(err) {
			return apperr.New(model.ErrorCodeNotFastForward,
				fmt.Sprintf("The remote has commits that %s does not. Pull them before pushing.", branch.ShortName))
		}
		return err
	})
	if err != nil {
		return err
	}
	return r.refresh(ctx, watch.Change{Refs: true})
}

// branchToSync finds the branch checked out in a worktree that is free to
// pull or push.
func (w *Workspace) branchToSync(worktreeID model.WorktreeID, verb string) (*openRepo, repo.WorktreeInfo, model.Ref, error) {
	r, wt, err := w.worktree(worktreeID)
	if err != nil {
		return nil, repo.WorktreeInfo{}, model.Ref{}, err
	}
	if wt.Operation != model.WorktreeOperationNone {
		return nil, repo.WorktreeInfo{}, model.Ref{}, apperr.New(model.ErrorCodeOperationInProgress,
			fmt.Sprintf("Finish or abort the %s in progress before you %s.", wt.Operation, verb)).
			WithDetail("operation", string(wt.Operation))
	}
	if wt.Head.Kind != model.HeadKindBranch {
		return nil, repo.WorktreeInfo{}, model.Ref{}, apperr.New(model.ErrorCodeInvalidArgument,
			fmt.Sprintf("Check out a branch before you %s.", verb))
	}
	branch, err := r.ref(wt.Head.RefID)
	if err != nil {
		return nil, repo.WorktreeInfo{}, model.Ref{}, err
	}
	return r, wt, branch, nil
}

// defaultPushRemote picks where to publish a branch that has no upstream:
// origin when there is one, otherwise the repository's only remote.
func (r *openRepo) defaultPushRemote(branch model.Ref) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, remote := range r.remotes {
		if remote.Name == "origin" {
			return remote.Name, nil
		}
	}
	switch len(r.remotes) {
	case 0:
		return "", apperr.New(model.ErrorCodeNoUpstream, "This repository has no remotes to push to.")
	case 1:
		return r.remotes[0].Name, nil
	}
	return "", apperr.New(model.ErrorCodeNoUpstream,
		fmt.Sprintf("%s has no upstream and there is no origin remote, so Forkit cannot tell where to push it.", branch.ShortName))
}
