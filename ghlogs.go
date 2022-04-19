package ghlogs

import (
	"context"
	"encoding/json"
	goerrors "errors"
	"github.com/aidansteele/ghal/labelgroup"
	"github.com/aidansteele/ghal/signalr"
	"github.com/google/go-github/v43/github"
	"github.com/pkg/errors"
	"golang.org/x/sync/singleflight"
	"io/ioutil"
	"net/http"
	"runtime/pprof"
	"sync"
	"syscall"
	"time"
)

type Ghlogs struct {
	api           *github.Client
	client        *http.Client
	userSessionId string
}

func New(api *github.Client, client *http.Client, userSessionId string) *Ghlogs {
	return &Ghlogs{
		api:           api,
		client:        client,
		userSessionId: userSessionId,
	}
}

type Run struct {
	Owner string
	Repo  string
	RunId int64
}

type RunOutput struct {
	Run             Run
	JobName         string
	StepName        string
	StepNumber      int
	AssumedStepName bool
	Lines           []string
}

func (ghl *Ghlogs) Logs(ctx context.Context, outch chan RunOutput, run Run) error {
	rs := &runStatus{
		Run:         run,
		StepNumbers: map[string]int64{},
		Statuses:    map[string]*jobStatus{},
		lock:        &sync.Mutex{},
	}

	err := ghl.populateRunStatus(ctx, rs)
	if err != nil {
		return err
	}

	if *rs.run.Status == "completed" {
		return nil // TODO: is this the right behaviour? or should we expose errJobDone?
	}

	anyJobName := ""
	for anyJobName == "" {
		for _, status := range rs.Statuses {
			if *status.Job.Status != "completed" {
				anyJobName = *status.Job.Name
				goto breakout
			}
		}

		time.Sleep(time.Second)
		err = ghl.populateRunStatus(ctx, rs)
		if err != nil {
			return err
		}
	}

breakout:
	sf := &singleflight.Group{}
	retryWithWsUrl := func(fn func(wsUrl string) error) error {
		for {
			wsUrl, err, _ := sf.Do("wsurl", func() (interface{}, error) {
				return ghl.getWsUrl(ctx, run, anyJobName)
			})
			if err != nil {
				return err
			}

			err = fn(wsUrl.(string))
			if !errors.Is(err, syscall.ECONNRESET) {
				// from github source code:
				/**
				 * AFD (Azure Front Door) abnormally closed the socket. This can happen after the websocket max time limit
				 * has been reached which is configured to 10 minutes for Actions. An abnormal closure can also sometimes
				 * randomly happen before the max time limit has been reached which stops incoming logs.
				 *
				 * Retrying with the existing socket is futile, but it is safe to discard the existing socket and create a new one.
				 */
				return err
			}
		}
	}

	g, ctx := labelgroup.WithContext(ctx)

	g.Go(ctx, pprof.Labels("work", "stepProgress"), func(ctx context.Context) error {
		return retryWithWsUrl(func(wsUrl string) error {
			return ghl.stepProgress(ctx, wsUrl, rs)
		})
	})

	ch := make(chan consoleOutputMessage)
	g.Go(ctx, pprof.Labels("work", "WatchRunAsync"), func(ctx context.Context) error {
		return retryWithWsUrl(func(wsUrl string) error {
			return signalr.Connect(ctx, wsUrl, "WatchRunAsync", ch)
		})
	})

	g.Go(ctx, pprof.Labels("work", "main loop"), func(ctx context.Context) error {
		return ghl.run(ctx, rs, ch, outch)
	})

	err = g.Wait()
	if errors.Cause(err) == errJobDone { // TODO: is this the right behaviour? or should we expose errJobDone?
		return nil
	}

	return err
}

func (ghl *Ghlogs) run(ctx context.Context, rs *runStatus, ch chan consoleOutputMessage, outch chan RunOutput) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ma := <-ch:
			job, step, certain := rs.step(ma.TimelineRecordId, ma.StepRecordId)
			jobName := "?"
			stepName := "?"
			stepNumber := 0

			if job != nil {
				jobName = *job.Name
			}

			if step != nil {
				stepName = *step.Name
				if *step.Number != 0 {
					stepNumber = int(*step.Number)
				}
			}

			outch <- RunOutput{
				Run:             rs.Run,
				JobName:         jobName,
				StepName:        stepName,
				StepNumber:      stepNumber,
				AssumedStepName: !certain,
				Lines:           ma.Lines,
			}
		}
	}
}

var errJobDone = goerrors.New("Job done")

func (ghl *Ghlogs) do(req *http.Request) (*http.Response, error) {
	if req.URL.Host == "github.com" {
		req.AddCookie(&http.Cookie{Name: "user_session", Value: ghl.userSessionId})
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Accept", "*/*")
	}

	req.Header.Set("User-Agent", "Ghlogs/0.1")

	response, err := ghl.client.Do(req)
	return response, errors.WithStack(err)
}

func getJson[T interface{}](ctx context.Context, ghl *Ghlogs, url string) (T, error) {
	var out T

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	response, err := ghl.do(req)
	if err != nil {
		return out, errors.WithStack(err)
	}

	if response.StatusCode > 299 {
		return out, errors.Errorf("unexpected status code: %s", response.Status)
	}

	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return out, errors.WithStack(err)
	}

	err = json.Unmarshal(body, &out)
	return out, errors.WithStack(err)
}

func (ghl *Ghlogs) stepProgress(ctx context.Context, wsUrl string, rs *runStatus) error {
	ticker := time.NewTicker(5 * time.Second)
	ch := make(chan []stepProgressUpdate)
	errch := make(chan error)

	go pprof.Do(ctx, pprof.Labels("inner", "WatchRunStepsProgressAsync"), func(ctx context.Context) {
		errch <- signalr.Connect(ctx, wsUrl, "WatchRunStepsProgressAsync", ch)
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errch:
			return err
		case <-ticker.C:
			err := ghl.populateRunStatus(ctx, rs)
			if err != nil {
				return err
			}

			anyNotComplete := false
			for _, status := range rs.Statuses {
				if *status.Job.Status != "completed" {
					anyNotComplete = true
				}
			}

			if !anyNotComplete {
				return errJobDone
			}
		case updates := <-ch:
			rs.lock.Lock()
			for _, update := range updates {
				rs.StepNumbers[update.StepRecordId] = update.StepNumber
			}
			rs.lock.Unlock()
		}
	}
}

func (ghl *Ghlogs) populateRunStatus(ctx context.Context, rs *runStatus) error {
	owner := rs.Run.Owner
	repo := rs.Run.Repo
	runId := rs.Run.RunId

	// TODO: pagination
	jobs, _, err := ghl.api.Actions.ListWorkflowJobs(ctx, owner, repo, runId, &github.ListWorkflowJobsOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	rs.run, _, err = ghl.api.Actions.GetWorkflowRunByID(ctx, owner, repo, runId)
	if err != nil {
		return errors.WithStack(err)
	}

	// TODO: pagination
	suite, _, err := ghl.api.Checks.ListCheckRunsCheckSuite(ctx, owner, repo, *rs.run.CheckSuiteID, &github.ListCheckRunsOptions{})
	if err != nil {
		return errors.WithStack(err)
	}

	rs.lock.Lock()
	defer rs.lock.Unlock()

	checkRunsById := map[int64]*github.CheckRun{}
	for _, cr := range suite.CheckRuns {
		cr := cr
		checkRunsById[*cr.ID] = cr
	}

	for _, job := range jobs.Jobs {
		job := job
		cr := checkRunsById[*job.ID]
		rs.Statuses[*cr.ExternalID] = &jobStatus{
			CheckRun: cr,
			Job:      job,
		}
	}

	return nil
}

type jobStatus struct {
	CheckRun *github.CheckRun
	Job      *github.WorkflowJob
}

type runStatus struct {
	Run Run

	Statuses    map[string]*jobStatus
	StepNumbers map[string]int64
	run         *github.WorkflowRun

	lock sync.Locker
}

func (rs *runStatus) step(timelineRecordId, stepRecordId string) (*github.WorkflowJob, *github.TaskStep, bool) {
	rs.lock.Lock()
	defer rs.lock.Unlock()

	js := rs.Statuses[timelineRecordId]
	if js == nil {
		return nil, nil, false
	}

	var inProgressStep *github.TaskStep

	number := rs.StepNumbers[stepRecordId]
	for _, step := range js.Job.Steps {
		stepNumber := *step.Number
		if stepNumber == number {
			return js.Job, step, true
		}

		inProgressStepNumber := int64(0)
		if inProgressStep != nil {
			inProgressStepNumber = *inProgressStep.Number
		}

		if stepNumber > inProgressStepNumber && *step.Status == "in_progress" {
			step := step
			inProgressStep = step
		}
	}

	return js.Job, inProgressStep, false
}
