package main

import (
	"context"
	"fmt"
	"github.com/aidansteele/ghal"
	"github.com/aidansteele/ghal/repoinfo"
	"github.com/aidansteele/ghal/runs"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/go-github/v43/github"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

var (
	version = "unknown" // set by goreleaser
)

type buildInfo struct {
	version string
	commit  string
	date    string
}

func readBuildInfo() buildInfo {
	info, _ := debug.ReadBuildInfo()
	commit := "unknown"
	date := "unknown"

	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			commit = setting.Value
		} else if setting.Key == "vcs.time" {
			date = setting.Value
		}
	}

	return buildInfo{
		version: version,
		commit:  commit,
		date:    date,
	}
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "version" {
		info := readBuildInfo()
		fmt.Printf(`
version: %s
commit: %s
build date: %s
`, info.version, info.commit, info.date)
		return
	}

	ctx := context.Background()

	repo := repoinfo.Info()

	client := http.DefaultClient
	api := github.NewClient(oauth2.NewClient(
		context.WithValue(ctx, oauth2.HTTPClient, client),
		oauth2.StaticTokenSource(&oauth2.Token{AccessToken: repo.Token}),
	))

	workflowFileName := os.Args[1]
	jobName := os.Args[2]

	ghl := ghlogs.New(
		api,
		client,
		os.Getenv("GITHUB_USER_SESSION"),
	)

	allRunsCh := make(chan *github.WorkflowRun)
	tailedRunsCh := make(chan *github.WorkflowRun)
	tailOutputCh := make(chan ghlogs.RunOutput)

	go runs.Monitor(ctx, api.Actions, allRunsCh, repo.Owner, repo.Repo, workflowFileName)
	go monitorRuns(ctx, ghl, allRunsCh, tailedRunsCh, tailOutputCh)
	tailOutput(tailedRunsCh, tailOutputCh, jobName)
}

func monitorRuns(ctx context.Context, ghl *ghlogs.Ghlogs, runch, tailedRunch chan *github.WorkflowRun, tailch chan ghlogs.RunOutput) {
	prevCancel := func() {}
	newCtx := ctx
	for run := range runch {
		prevCancel()
		newCtx, prevCancel = context.WithCancel(ctx)

		go func(ctx context.Context, run *github.WorkflowRun) {
			tailedRunch <- run
			err := ghl.Logs(ctx, tailch, ghlogs.Run{
				Owner: *run.Repository.Owner.Login,
				Repo:  *run.Repository.Name,
				RunId: *run.ID,
			})

			ctxerr := ctx.Err()
			cause := errors.Cause(err)
			if err != nil && cause != ctxerr {
				fmt.Printf("%+v\n", err)
				panic(err)
			}
		}(newCtx, run)
	}
}

type stepNameOffset struct {
	stepName string
	offset   int
}

type model struct {
	wfRun     *github.WorkflowRun
	jobName   string
	stepNames []stepNameOffset

	tailch   chan ghlogs.RunOutput
	runch    chan *github.WorkflowRun
	buffer   *strings.Builder
	ready    bool
	viewport viewport.Model
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.waitForActivity(),
		tick(time.Second),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var (
		cmd  tea.Cmd
		cmds []tea.Cmd
	)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		// These keys should exit the program.
		case "ctrl+c", "q":
			return m, tea.Quit
		}
	case tickMsg:
		return m, tick(time.Second)
	case *github.WorkflowRun:
		m.wfRun = msg
		m.reset()
		cmds = append(cmds, m.waitForActivity())
	case ghlogs.RunOutput:
		m.append(msg)
		m.viewport.SetContent(m.buffer.String())
		m.viewport.GotoBottom()
		cmds = append(cmds, m.waitForActivity())
	case tea.WindowSizeMsg:
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := lipgloss.Height(m.footerView())
		verticalMarginHeight := headerHeight + footerHeight

		if !m.ready {
			// Since this program is using the full size of the viewport we
			// need to wait until we've received the window dimensions before
			// we can initialize the viewport. The initial dimensions come in
			// quickly, though asynchronously, which is why we wait for them
			// here.
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMarginHeight)
			m.viewport.YPosition = headerHeight
			m.viewport.HighPerformanceRendering = false
			m.viewport.SetContent(m.buffer.String())
			m.ready = true

			// This is only necessary for high performance rendering, which in
			// most cases you won't need.
			//
			// Render the viewport one line below the header.
			m.viewport.YPosition = headerHeight + 1
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMarginHeight
		}
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *model) reset() {
	m.buffer.Reset()
	m.viewport.SetContent("")
	m.stepNames = []stepNameOffset{{stepName: "", offset: -1}}
}

func (m *model) append(output ghlogs.RunOutput) {
	if output.Run.RunId != *m.wfRun.ID {
		return
	}

	if output.JobName != m.jobName {
		return
	}

	stepName := output.StepName
	if output.AssumedStepName {
		stepName += "*"
	}

	for _, line := range output.Lines {
		fmt.Fprintln(m.buffer, line)
	}

	latestStep := m.stepNames[len(m.stepNames)-1].stepName
	if stepName != latestStep {
		offset := len(strings.Split(m.buffer.String(), "\n"))
		m.stepNames = append(m.stepNames, stepNameOffset{
			stepName: stepName,
			offset:   offset,
		})
	}
}

var titleStyle = func() lipgloss.Style {
	b := lipgloss.RoundedBorder()
	b.Right = "├"
	return lipgloss.NewStyle().
		BorderStyle(b).
		Padding(0, 1).
		Foreground(lipgloss.Color("#067D17"))
	//Foreground(lipgloss.AdaptiveColor{
	//	Light: "#067D17",
	//	Dark:  "#82ED7D",
	//})
}()

func (m model) headerView() string {
	yoff := m.viewport.YOffset
	stepName := m.stepNames[len(m.stepNames)-1].stepName
	for _, name := range m.stepNames {
		if name.offset < yoff {
			stepName = name.stepName
		}
	}

	wfName := ""
	runNumber := -1
	if m.wfRun != nil {
		wfName = *m.wfRun.Name
		runNumber = *m.wfRun.RunNumber
	}

	title := titleStyle.Render(fmt.Sprintf("%s / %s / %s (#%d)", wfName, m.jobName, stepName, runNumber))
	line := strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(title)))
	return lipgloss.JoinHorizontal(lipgloss.Center, title, line)
}

var infoStyle = func() lipgloss.Style {
	b := lipgloss.RoundedBorder()
	b.Left = "┤"
	return lipgloss.NewStyle().BorderStyle(b).Padding(0, 1)
}()

func (m model) footerView() string {
	duration := "-"
	if m.wfRun != nil {
		dur := time.Now().Sub(m.wfRun.RunStartedAt.Time).Truncate(time.Second)
		duration = dur.String()
	}

	info := infoStyle.Render(duration)
	line := strings.Repeat("─", max(0, m.viewport.Width-lipgloss.Width(info)))
	return lipgloss.JoinHorizontal(lipgloss.Center, line, info)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (m model) View() string {
	if !m.ready {
		return "\n  Initializing..."
	}
	return fmt.Sprintf("%s\n%s\n%s", m.headerView(), m.viewport.View(), m.footerView())
}

func (m model) waitForActivity() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.tailch:
			return msg
		case msg := <-m.runch:
			return msg
		}
	}
}

type tickMsg int

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(_ time.Time) tea.Msg {
		return tickMsg(0)
	})
}

func tailOutput(runch chan *github.WorkflowRun, ch chan ghlogs.RunOutput, jobName string) {
	m := model{
		jobName: jobName,

		runch:    runch,
		tailch:   ch,
		buffer:   &strings.Builder{},
		viewport: viewport.Model{},
	}
	m.reset()

	p := tea.NewProgram(m)
	err := p.Start()
	if err != nil {
		fmt.Printf("%+v\n", err)
		panic(err)
	}
}
