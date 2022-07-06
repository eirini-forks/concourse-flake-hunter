package hunter

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/go-concourse/concourse"
	"github.com/eirini-forks/concourse-flake-hunter/fly"
)

const (
	StatusForbidden = "forbidden"
	WorkerPoolSize  = 8
)

type SearchSpec struct {
	Pattern     *regexp.Regexp
	ShowOneOffs bool
	MaxAge      int
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
	go s.getBuildsFromPage(flakesChan, concourse.Page{Limit: 30000}, spec)
	return flakesChan
}

func (s *Searcher) getBuildsFromPage(flakesChan chan Build, page concourse.Page, spec SearchSpec) {
	var (
		buildsChan = make(chan atc.Build)
		wg         sync.WaitGroup
	)

	wg.Add(WorkerPoolSize)
	for i := 0; i < WorkerPoolSize; i++ {
		go func() {
			s.processBuilds(flakesChan, buildsChan, spec)
			wg.Done()
		}()
	}

	s.fetchBuildsFromPage(buildsChan, page, spec)
	close(buildsChan)

	wg.Wait()
	close(flakesChan)
}

func (s *Searcher) fetchBuildsFromPage(buildsChan chan atc.Build, page concourse.Page, spec SearchSpec) {
	var pages = concourse.Pagination{Next: &page}
	var builds []atc.Build
	var err error

	for pages.Next != nil {
		page = *pages.Next
		builds, pages, err = s.client.Builds(page)
		if err != nil {
			println(err.Error())
			continue
		}

		for _, build := range builds {
			if build.Status != string(atc.StatusFailed) {
				continue
			}

			if !spec.ShowOneOffs && isOneOff(build) {
				continue
			}

			if spec.MaxAge > 0 && age(build) > spec.MaxAge {
				return
			}

			buildsChan <- build
		}
	}
}

func (s *Searcher) processBuilds(flakesCh chan Build, buildsCh chan atc.Build, spec SearchSpec) {
	for build := range buildsCh {
		if err := s.processBuild(flakesCh, build, spec); err != nil {
			println(err.Error())
			continue
		}
	}
}

func age(build atc.Build) int {
	endTime := time.Unix(build.EndTime, 0)
	return int(time.Since(endTime) / time.Hour)
}

func isOneOff(build atc.Build) bool {
	return build.JobName == ""
}

func (s *Searcher) processBuild(flakesCh chan Build, build atc.Build, spec SearchSpec) error {
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
