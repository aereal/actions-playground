package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/google/go-github/v45/github"
	"github.com/hashicorp/go-multierror"
	"golang.org/x/oauth2"
	"golang.org/x/sync/semaphore"
)

func main() {
	if err := run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var (
	owner    string
	repo     string
	prNumber int

	//go:embed search-deployments.gql
	searchDeploymentsQuery string
)

func run() error {
	flag.StringVar(&owner, "owner", "", "repository owner")
	flag.StringVar(&repo, "repo", "", "repository name")
	flag.IntVar(&prNumber, "pr-number", 0, "pull request number")
	flag.Parse()
	if owner == "" {
		return errors.New("-owner is required")
	}
	if repo == "" {
		return errors.New("-repo is required")
	}
	if prNumber == 0 {
		return errors.New("-pr-number is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	httpClient := buildAuthenHTTPClient(ctx)
	checkSuites, err := fetchAbandonedCheckSuites(ctx, httpClient)
	if err != nil {
		return err
	}
	if len(checkSuites) == 0 {
		log.Printf("no check suites found")
		return nil
	}
	ghClient := github.NewClient(httpClient)
	var deployments []deployment
	for _, checkSuite := range checkSuites {
		for _, run := range checkSuite.CheckRuns.Nodes {
			if run.Deployment != nil {
				deployments = append(deployments, *run.Deployment)
			}
		}
	}
	if err := cancelDeployments(ctx, ghClient, deployments); err != nil {
		return err
	}
	pendingDeploymentReviewRequests := checkSuiteConnection(checkSuites).pendingDeploymentReviewRequests()
	log.Printf("%d pending deployments found", len(pendingDeploymentReviewRequests))
	for id, reqs := range pendingDeploymentReviewRequests {
		if pendingDeployments, _ := getPendingDeployments(ctx, ghClient, id); len(deployments) > 0 {
			for _, env := range pendingDeployments {
				log.Printf("pending deployment environment: name=%s id=%d", env.GetName(), env.GetID())
			}
		}
		buf := new(bytes.Buffer)
		fmt.Fprintf(buf, "reject pending deployment %d: ", id)
		var seen bool
		for _, r := range reqs {
			if seen {
				fmt.Fprint(buf, ", ")
			} else {
				seen = true
			}
			fmt.Fprintf(buf, "environment=%d", r.Environment.ID)
		}
		log.Print(buf.String())
		if err := rejectPendingDeployments(ctx, ghClient, id, reqs); err != nil {
			return err
		}
	}
	return nil
}

func cancelDeployments(ctx context.Context, ghClient *github.Client, deployments []deployment) error {
	log.Printf("%d deployments to be cancelled", len(deployments))
	sem := semaphore.NewWeighted(4)
	group := &multierror.Group{}
	for _, deployment := range deployments {
		log.Printf("try to cancel deployment %d (%s)", deployment.ID, deployment.Environment)
		if err := sem.Acquire(ctx, 1); err != nil {
			return err
		}
		deployment := deployment
		group.Go(func() error {
			defer sem.Release(1)
			req := &github.DeploymentStatusRequest{
				State:       github.String("error"),
				Description: github.String(fmt.Sprintf("corresponding PR #%d is abandoned", prNumber)),
				Environment: github.String(deployment.Environment),
			}
			_, _, err := ghClient.Repositories.CreateDeploymentStatus(ctx, owner, repo, deployment.ID, req)
			return err
		})
	}
	merr := group.Wait()
	if merr == nil {
		return nil
	}
	return merr.ErrorOrNil()
}

type reviewPendingDeploymentsRequest struct {
	EnvironmentIDs []int64 `json:"environment_ids"`
	State          string  `json:"state"`
	Comment        string  `json:"comment"`
}

type reviewPendingDeploymentsResponse []github.Deployment

func rejectPendingDeployments(ctx context.Context, ghClient *github.Client, workflowRunID int64, deployReqs []deploymentRequest) error {
	reqURL := fmt.Sprintf("repos/%s/%s/actions/runs/%d/pending_deployments", owner, repo, workflowRunID)
	payload := reviewPendingDeploymentsRequest{
		State:          "rejected",
		Comment:        "corresponding Pull Request was closed; so waiting deployments are abondoned",
		EnvironmentIDs: make([]int64, len(deployReqs)),
	}
	for i, deployReq := range deployReqs {
		payload.EnvironmentIDs[i] = deployReq.Environment.ID
	}
	log.Printf("POST %s %#v", reqURL, payload)
	req, err := ghClient.NewRequest(http.MethodPost, reqURL, payload)
	if err != nil {
		return err
	}
	var resp reviewPendingDeploymentsResponse
	if _, err := ghClient.Do(ctx, req, &resp); err != nil {
		return err
	}
	return nil
}

func getPendingDeployments(ctx context.Context, ghClient *github.Client, workflowRunID int64) ([]*github.Environment, error) {
	reqURL := fmt.Sprintf("repos/%s/%s/actions/runs/%d/pending_deployments", owner, repo, workflowRunID)
	req, err := ghClient.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	var deployments []struct {
		Environment *github.Environment `json:"environment"`
	}
	if _, err := ghClient.Do(ctx, req, &deployments); err != nil {
		return nil, err
	}
	ret := make([]*github.Environment, len(deployments))
	for i, d := range deployments {
		ret[i] = d.Environment
	}
	return ret, nil
}

func fetchAbandonedCheckSuites(ctx context.Context, httpClient *http.Client) ([]checkSuite, error) {
	p := graphqlRequest[fetchWaitingDeploymentsQueryVariables]{
		Query: searchDeploymentsQuery,
		Variables: fetchWaitingDeploymentsQueryVariables{
			Owner:             owner,
			Repo:              repo,
			PullRequestNumber: prNumber,
		},
	}
	body, err := doQuery(ctx, httpClient, p)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var r graphqlResponse[fetchWaitingDeploymentsQueryResponse]
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return nil, err
	}
	return r.Data.Repository.PullRequest.HeadRef.Target.CheckSuites.Nodes, nil
}

func doQuery[V any](ctx context.Context, httpClient *http.Client, payload graphqlRequest[V]) (io.ReadCloser, error) {
	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(payload); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/graphql", buf)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("status code is %d", resp.StatusCode)
	}
	return resp.Body, nil
}

type graphqlRequest[V any] struct {
	Query     string `json:"query"`
	Variables V      `json:"variables"`
}

type fetchWaitingDeploymentsQueryVariables struct {
	Owner             string `json:"owner"`
	Repo              string `json:"name"`
	PullRequestNumber int    `json:"prNumber"`
}

type deployment struct {
	Environment string `json:"environment"`
	ID          int64  `json:"databaseId"`
}

type checkSuiteConnection []checkSuite

func (c checkSuiteConnection) pendingDeploymentReviewRequests() map[int64][]deploymentRequest {
	ret := map[int64][]deploymentRequest{}
	for _, checkSuite := range c {
		id := checkSuite.WorkflowRun.ID
		reqs := checkSuite.WorkflowRun.PendingDeploymentRequests.Nodes
		if len(reqs) == 0 {
			continue
		}
		ret[id] = append(ret[id], reqs...)
	}
	return ret
}

type checkSuite struct {
	CheckRuns struct {
		Nodes []struct {
			Deployment *deployment `json:"deployment"`
		} `json:"nodes"`
	} `json:"checkRuns"`
	WorkflowRun struct {
		ID                        int64                              `json:"databaseId"`
		PendingDeploymentRequests pendingDeploymentRequestConnection `json:"pendingDeploymentRequests"`
	} `json:"workflowRun"`
}

type deploymentRequest struct {
	Environment *struct {
		ID int64 `json:"databaseId"`
	} `json:"environment"`
}

type pendingDeploymentRequestConnection struct {
	TotalCount int64               `json:"totalCount"`
	Nodes      []deploymentRequest `json:"nodes"`
}

type fetchWaitingDeploymentsQueryResponse struct {
	Repository struct {
		PullRequest struct {
			HeadRef struct {
				Target struct {
					CheckSuites struct {
						Nodes []checkSuite `json:"nodes"`
					} `json:"checkSuites"`
				} `json:"target"`
			} `json:"headRef"`
		} `json:"pullRequest"`
	} `json:"repository"`
}

type graphqlResponse[V any] struct {
	Data V `json:"data"`
}

func buildAuthenHTTPClient(ctx context.Context) *http.Client {
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")}))
}
