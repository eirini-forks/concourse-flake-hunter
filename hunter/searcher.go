package hunter

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"

	"github.com/concourse/atc"
	"github.com/concourse/go-concourse/concourse"
	"github.com/masters-of-cats/concourse-flake-hunter/fly"
)

const (
	StatusForbidden = "forbidden"
	WorkerPoolSize  = 8
)

type SearchSpec struct {
	Pattern     *regexp.Regexp
	ShowOneOffs bool
}

type Searcher struct {
	client fly.Client
}

type Build struct {
	atc.Build
	ConcourseURL string
	Matches      []string
}

func NewSearcher(client fly.Client) *Searcher {
	return &Searcher{
		client: client,
	}
}

func (s *Searcher) Search(spec SearchSpec) chan Build {
	flakesChan := make(chan Build, 100)
	go s.getBuildsFromPage(flakesChan, concourse.Page{Limit: 300}, spec)
	return flakesChan
}

func (s *Searcher) getBuildsFromPage(flakesChan chan Build, page concourse.Page, spec SearchSpec) {
	var (
		buildsChan = make(chan []atc.Build)
		pages      = concourse.Pagination{Next: &page}
		builds     []atc.Build
		err        error
		wg         sync.WaitGroup
	)

	wg.Add(WorkerPoolSize)
	for i := 0; i < WorkerPoolSize; i++ {
		go func() {
			s.processBuilds(flakesChan, buildsChan, spec)
			wg.Done()
		}()
	}

	for pages.Next != nil {
		page = *pages.Next
		builds, pages, err = s.client.Builds(page)
		if err != nil {
			println(err.Error())
			continue
		}

		buildsChan <- builds
	}
	close(buildsChan)

	wg.Wait()
	close(flakesChan)
}

func (s *Searcher) processBuilds(flakesCh chan Build, buildsCh chan []atc.Build, spec SearchSpec) {
	for builds := range buildsCh {
		for _, build := range builds {
			if !spec.ShowOneOffs && isOneOff(build) {
				continue
			}

			if err := s.processBuild(flakesCh, build, spec); err != nil {
				println(err.Error())
				continue
			}
		}
	}
}

func isOneOff(build atc.Build) bool {
	return build.JobName == ""
}

func (s *Searcher) processBuild(flakesCh chan Build, build atc.Build, spec SearchSpec) error {
	if build.Status != string(atc.StatusFailed) {
		// We only care about failed builds
		return nil
	}

	events, err := s.client.BuildEvents(strconv.Itoa(build.ID))
	// Not sure why, but concourse.Builds returns builds from other teams
	if err != nil && err.Error() != StatusForbidden {
		return errors.New("Failed to get build events")
	}

	matches := spec.Pattern.FindAllString(string(events), -1)

	if len(matches) > 0 {
		b := Build{build, s.buildBuildURL(build), matches}
		flakesCh <- b
	}
	return nil
}

func (s *Searcher) buildBuildURL(build atc.Build) string {
	return fmt.Sprintf("%s/teams/%s/pipelines/%s/jobs/%s/builds/%s", s.client.ConcourseURL(), build.TeamName, build.PipelineName, build.JobName, build.Name)
}
