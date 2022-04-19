package ghlogs

import (
	"context"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/pkg/errors"
	"net/http"
	"time"
)

type liveLogsResponse struct {
	Success bool          `json:"success"`
	Errors  []interface{} `json:"errors"`
	Data    struct {
		AuthenticatedUrl string `json:"authenticated_url"`
	} `json:"data"`
}

type liveLogsSecondResponse struct {
	LogStreamWebSocketUrl string `json:"logStreamWebSocketUrl"`
}

func (ghl *Ghlogs) getWsUrl(ctx context.Context, run Run, jobName string) (string, error) {
	refreshUrl := ""

	for attempts := 0; attempts < 10; attempts++ {
		//fmt.Printf("part a %d\n", attempts)

		r1, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://github.com/%s/%s/actions/runs/%d/graph/job/%s", run.Owner, run.Repo, run.RunId, jobName), nil)
		resp1, err := ghl.do(r1)
		if err != nil {
			return "", err
		}

		doc, err := goquery.NewDocumentFromReader(resp1.Body)
		if err != nil {
			return "", err
		}

		selx := doc.Find("streaming-graph-job")
		if concluded, _ := selx.Attr("data-concluded"); concluded == "true" {
			return "", errors.WithStack(errJobDone)
		}

		refreshRelUrl, _ := selx.Attr("data-streaming-url")
		if refreshRelUrl != "" {
			refreshUrl = fmt.Sprintf("https://github.com%s", refreshRelUrl)
			break
		}

		time.Sleep(time.Second)
	}

	for attempts := 0; attempts < 10; attempts++ {
		//fmt.Printf("part b %d\n", attempts)

		ret1, _ := getJson[liveLogsResponse](ctx, ghl, refreshUrl)
		nextUrl := ret1.Data.AuthenticatedUrl
		if nextUrl == "" {
			time.Sleep(time.Second)
			continue
		}

		ret2, _ := getJson[liveLogsSecondResponse](ctx, ghl, nextUrl)
		if u := ret2.LogStreamWebSocketUrl; u != "" {
			return u, nil
		}

		time.Sleep(time.Second)
	}

	return "", errors.New("didn't get url after multiple attempts")
}
