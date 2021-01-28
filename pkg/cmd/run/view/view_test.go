package view

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"
	"time"

	"github.com/cli/cli/internal/ghrepo"
	"github.com/cli/cli/pkg/cmd/run/shared"
	"github.com/cli/cli/pkg/cmdutil"
	"github.com/cli/cli/pkg/httpmock"
	"github.com/cli/cli/pkg/iostreams"
	"github.com/cli/cli/pkg/prompt"
	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
)

func TestNewCmdView(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		tty      bool
		wants    ViewOptions
		wantsErr bool
	}{
		{
			name:     "blank nontty",
			wantsErr: true,
		},
		{
			name: "blank tty",
			tty:  true,
			wants: ViewOptions{
				Prompt:       true,
				ShowProgress: true,
			},
		},
		{
			name: "exit status",
			cli:  "-e 1234",
			wants: ViewOptions{
				RunID:      "1234",
				ExitStatus: true,
			},
		},
		{
			name: "verbosity",
			cli:  "-v",
			tty:  true,
			wants: ViewOptions{
				Verbose:      true,
				Prompt:       true,
				ShowProgress: true,
			},
		},
		{
			name: "with arg nontty",
			cli:  "1234",
			wants: ViewOptions{
				RunID: "1234",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			io, _, _, _ := iostreams.Test()
			io.SetStdinTTY(tt.tty)
			io.SetStdoutTTY(tt.tty)

			f := &cmdutil.Factory{
				IOStreams: io,
			}

			argv, err := shlex.Split(tt.cli)
			assert.NoError(t, err)

			var gotOpts *ViewOptions
			cmd := NewCmdView(f, func(opts *ViewOptions) error {
				gotOpts = opts
				return nil
			})
			cmd.SetArgs(argv)
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(ioutil.Discard)
			cmd.SetErr(ioutil.Discard)

			_, err = cmd.ExecuteC()
			if tt.wantsErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			assert.Equal(t, tt.wants.RunID, gotOpts.RunID)
			assert.Equal(t, tt.wants.ShowProgress, gotOpts.ShowProgress)
			assert.Equal(t, tt.wants.Prompt, gotOpts.Prompt)
			assert.Equal(t, tt.wants.ExitStatus, gotOpts.ExitStatus)
			assert.Equal(t, tt.wants.Verbose, gotOpts.Verbose)
		})
	}
}

func TestViewRun(t *testing.T) {
	created, _ := time.Parse("2006-01-02 15:04:05", "2021-02-23 04:51:00")
	updated, _ := time.Parse("2006-01-02 15:04:05", "2021-02-23 04:55:34")
	testRun := func(name string, id int, s shared.Status, c shared.Conclusion) shared.Run {
		return shared.Run{
			Name:       name,
			ID:         id,
			CreatedAt:  created,
			UpdatedAt:  updated,
			Status:     s,
			Conclusion: c,
			Event:      "push",
			HeadBranch: "trunk",
			JobsURL:    fmt.Sprintf("/runs/%d/jobs", id),
			HeadCommit: shared.Commit{
				Message: "cool commit",
			},
			HeadSha: "1234567890",
			URL:     fmt.Sprintf("runs/%d", id),
		}
	}

	successfulRun := testRun("successful", 3, shared.Completed, shared.Success)
	failedRun := testRun("failed", 1234, shared.Completed, shared.Failure)

	runs := []shared.Run{
		testRun("timed out", 3, shared.Completed, shared.TimedOut),
		testRun("in progress", 2, shared.InProgress, ""),
		successfulRun,
		testRun("cancelled", 4, shared.Completed, shared.Cancelled),
		failedRun,
		testRun("neutral", 6, shared.Completed, shared.Neutral),
		testRun("skipped", 7, shared.Completed, shared.Skipped),
		testRun("requested", 8, shared.Requested, ""),
		testRun("queued", 9, shared.Queued, ""),
		testRun("stale", 10, shared.Completed, shared.Stale),
	}

	successfulJob := shared.Job{
		ID:          10,
		Status:      shared.Completed,
		Conclusion:  shared.Success,
		Name:        "cool job",
		StartedAt:   created,
		CompletedAt: updated,
		Steps: []shared.Step{
			{
				Name:       "fob the barz",
				Status:     shared.Completed,
				Conclusion: shared.Success,
				Number:     1,
			},
			{
				Name:       "barz the fob",
				Status:     shared.Completed,
				Conclusion: shared.Success,
				Number:     2,
			},
		},
	}

	failedJob := shared.Job{
		ID:          20,
		Status:      shared.Completed,
		Conclusion:  shared.Failure,
		Name:        "sad job",
		StartedAt:   created,
		CompletedAt: updated,
		Steps: []shared.Step{
			{
				Name:       "barf the quux",
				Status:     shared.Completed,
				Conclusion: shared.Success,
				Number:     1,
			},
			{
				Name:       "quux the barf",
				Status:     shared.Completed,
				Conclusion: shared.Failure,
				Number:     2,
			},
		},
	}

	failedJobAnnotations := []shared.Annotation{
		{
			JobName:   "sad job",
			Message:   "the job is sad",
			Path:      "blaze.py",
			Level:     "failure",
			StartLine: 420,
		},
	}

	tests := []struct {
		name     string
		stubs    func(*httpmock.Registry)
		askStubs func(*prompt.AskStubber)
		opts     *ViewOptions
		tty      bool
		wantErr  bool
		wantOut  string
	}{
		{
			name: "associate with PR",
			tty:  true,
			opts: &ViewOptions{
				RunID:        "3",
				Prompt:       false,
				ShowProgress: true,
			},
			stubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs/3"),
					httpmock.JSONResponse(successfulRun))
				reg.Register(
					httpmock.GraphQL(`query PullRequestForRun`),
					httpmock.StringResponse(`{"data": {
            "repository": {
                "pullRequests": {
                    "nodes": [
                        {"number": 2898}
                    ]}}}}`))
				reg.Register(
					httpmock.REST("GET", "runs/3/jobs"),
					httpmock.JSONResponse(shared.JobsPayload{
						Jobs: []shared.Job{
							successfulJob,
						},
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/10/annotations"),
					httpmock.JSONResponse([]shared.Annotation{}))
			},
			wantOut: "\n✓ trunk successful #2898 · 3\nTriggered via push about 59 minutes ago\n\nJOBS\n✓ cool job (ID 10)\n\nFor more information about a job, try: gh job view <job-id>\nview this run on GitHub: runs/3\n",
		},
		{
			name: "exit status, failed run",
			opts: &ViewOptions{
				RunID:      "1234",
				ExitStatus: true,
			},
			stubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs/1234"),
					httpmock.JSONResponse(failedRun))
				reg.Register(
					httpmock.GraphQL(`query PullRequestForRun`),
					httpmock.StringResponse(``))
				reg.Register(
					httpmock.REST("GET", "runs/1234/jobs"),
					httpmock.JSONResponse(shared.JobsPayload{
						Jobs: []shared.Job{
							failedJob,
						},
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/20/annotations"),
					httpmock.JSONResponse(failedJobAnnotations))
			},
			wantOut: "\nX trunk failed · 1234\nTriggered via push about 59 minutes ago\n\nJOBS\nX sad job (ID 20)\n  ✓ barf the quux\n  X quux the barf\n\nANNOTATIONS\nX the job is sad\nsad job: blaze.py#420\n\n\nFor more information about a job, try: gh job view <job-id>\nview this run on GitHub: runs/1234\n",
			wantErr: true,
		},
		{
			name: "exit status, successful run",
			opts: &ViewOptions{
				RunID:      "3",
				ExitStatus: true,
			},
			stubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs/3"),
					httpmock.JSONResponse(successfulRun))
				reg.Register(
					httpmock.GraphQL(`query PullRequestForRun`),
					httpmock.StringResponse(``))
				reg.Register(
					httpmock.REST("GET", "runs/3/jobs"),
					httpmock.JSONResponse(shared.JobsPayload{
						Jobs: []shared.Job{
							successfulJob,
						},
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/10/annotations"),
					httpmock.JSONResponse([]shared.Annotation{}))
			},
			wantOut: "\n✓ trunk successful · 3\nTriggered via push about 59 minutes ago\n\nJOBS\n✓ cool job (ID 10)\n\nFor more information about a job, try: gh job view <job-id>\nview this run on GitHub: runs/3\n",
		},
		{
			name: "verbose",
			tty:  true,
			opts: &ViewOptions{
				RunID:        "1234",
				Prompt:       false,
				ShowProgress: true,
				Verbose:      true,
			},
			stubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs/1234"),
					httpmock.JSONResponse(failedRun))
				reg.Register(
					httpmock.GraphQL(`query PullRequestForRun`),
					httpmock.StringResponse(``))
				reg.Register(
					httpmock.REST("GET", "runs/1234/jobs"),
					httpmock.JSONResponse(shared.JobsPayload{
						Jobs: []shared.Job{
							successfulJob,
							failedJob,
						},
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/10/annotations"),
					httpmock.JSONResponse([]shared.Annotation{}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/20/annotations"),
					httpmock.JSONResponse(failedJobAnnotations))
			},
			wantOut: "\nX trunk failed · 1234\nTriggered via push about 59 minutes ago\n\nJOBS\n✓ cool job (ID 10)\n  ✓ fob the barz\n  ✓ barz the fob\nX sad job (ID 20)\n  ✓ barf the quux\n  X quux the barf\n\nANNOTATIONS\nX the job is sad\nsad job: blaze.py#420\n\n\nFor more information about a job, try: gh job view <job-id>\nview this run on GitHub: runs/1234\n",
		},
		{
			name: "prompts for choice",
			tty:  true,
			stubs: func(reg *httpmock.Registry) {
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs"),
					httpmock.JSONResponse(shared.RunsPayload{
						WorkflowRuns: runs,
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/actions/runs/3"),
					httpmock.JSONResponse(successfulRun))
				reg.Register(
					httpmock.GraphQL(`query PullRequestForRun`),
					httpmock.StringResponse(``))
				reg.Register(
					httpmock.REST("GET", "runs/3/jobs"),
					httpmock.JSONResponse(shared.JobsPayload{
						Jobs: []shared.Job{
							{
								ID:          10,
								Status:      shared.Completed,
								Conclusion:  shared.Success,
								Name:        "cool job",
								StartedAt:   created,
								CompletedAt: updated,
							},
						},
					}))
				reg.Register(
					httpmock.REST("GET", "repos/OWNER/REPO/check-runs/10/annotations"),
					httpmock.JSONResponse([]shared.Annotation{}))
			},
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(2)
			},
			opts: &ViewOptions{
				Prompt:       true,
				ShowProgress: true,
			},
			wantOut: "\n✓ trunk successful · 3\nTriggered via push about 59 minutes ago\n\nJOBS\n✓ cool job (ID 10)\n\nFor more information about a job, try: gh job view <job-id>\nview this run on GitHub: runs/3\n",
		},
	}

	for _, tt := range tests {
		reg := &httpmock.Registry{}
		tt.stubs(reg)
		tt.opts.HttpClient = func() (*http.Client, error) {
			return &http.Client{Transport: reg}, nil
		}

		tt.opts.Now = func() time.Time {
			notnow, _ := time.Parse("2006-01-02 15:04:05", "2021-02-23 05:50:00")
			return notnow
		}

		io, _, stdout, _ := iostreams.Test()
		io.SetStdoutTTY(tt.tty)
		tt.opts.IO = io
		tt.opts.BaseRepo = func() (ghrepo.Interface, error) {
			return ghrepo.FromFullName("OWNER/REPO")
		}

		as, teardown := prompt.InitAskStubber()
		defer teardown()
		if tt.askStubs != nil {
			tt.askStubs(as)
		}

		t.Run(tt.name, func(t *testing.T) {
			err := runView(tt.opts)
			if tt.wantErr {
				assert.Error(t, err)
				if !tt.opts.ExitStatus {
					return
				}
			}
			if !tt.opts.ExitStatus {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.wantOut, stdout.String())
			reg.Verify(t)
		})
	}
}
