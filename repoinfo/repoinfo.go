package repoinfo

import (
	"fmt"
	"github.com/cli/cli/v2/api"
	"github.com/cli/cli/v2/pkg/cmd/factory"
	"os"
)

type RepoInfo struct {
	Owner string
	Repo  string
	Token string

	apic *api.Client
}

func (r RepoInfo) RepoName() string {
	return r.Repo
}

func (r RepoInfo) RepoOwner() string {
	return r.Owner
}

func (r RepoInfo) RepoHost() string {
	return "github.com"
}

func Info() *RepoInfo {
	f := factory.New("1")
	s := factory.SmartBaseRepoFunc(f)
	repo, err := s()
	if err != nil {
		fmt.Printf("%+v\n", err)
		panic(err)
	}

	owner := repo.RepoOwner()
	name := repo.RepoName()
	host := repo.RepoHost()

	cfg, err := f.Config()
	if err != nil {
		fmt.Printf("%+v\n", err)
		panic(err)
	}

	hc, err := f.HttpClient()
	if err != nil {
		fmt.Printf("%+v\n", err)
		panic(err)
	}

	apic := api.NewClientFromHTTP(hc)

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token, err = cfg.Get(host, "oauth_token")
		if err != nil {
			fmt.Printf("%+v\n", err)
			panic(err)
		}
	}

	return &RepoInfo{
		Owner: owner,
		Repo:  name,
		Token: token,
		apic:  apic,
	}
}
