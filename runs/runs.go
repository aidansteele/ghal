package runs

import (
	"context"
	"fmt"
	"github.com/google/go-github/v43/github"
	"time"
)

type Lister interface {
	ListRepositoryWorkflowRuns(ctx context.Context, owner, repo string, opts *github.ListWorkflowRunsOptions) (*github.WorkflowRuns, *github.Response, error)
	ListWorkflowRunsByFileName(ctx context.Context, owner, repo, filename string, opts *github.ListWorkflowRunsOptions) (*github.WorkflowRuns, *github.Response, error)
}

func Monitor(ctx context.Context, lister Lister, ch chan *github.WorkflowRun, owner, repo, filename string) {
	seenRunIds := map[int64]struct{}{}
	ticker := time.NewTicker(4 * time.Second)

	opts := &github.ListWorkflowRunsOptions{
		//Actor:       "aidansteele",
		ListOptions: github.ListOptions{PerPage: 10},
		//Branch:  "",
		//Event:   "",
		//Status:  "",
		//Created: "",
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			wfRuns, _, err := lister.ListWorkflowRunsByFileName(ctx, owner, repo, filename, opts)
			if err != nil {
				fmt.Printf("%+v\n", err)
				panic(err)
			}

			s := wfRuns.WorkflowRuns
			for i := len(s) - 1; i >= 0; i-- {
				run := s[i]
				if _, seen := seenRunIds[*run.ID]; !seen {
					seenRunIds[*run.ID] = struct{}{}

					if *run.Status != "completed" {
						ch <- run
					}
				}
			}
		}
	}

	//g.Actions.ListWorkflowRunsByID(ctx, owner, repo, 0, opts)
	//g.Actions.ListWorkflowRunsByFileName(ctx, owner, repo, "", opts)
}
