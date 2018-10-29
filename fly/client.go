package fly

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/concourse/atc"
	"github.com/concourse/atc/event"
	"github.com/concourse/fly/rc"
	"github.com/concourse/go-concourse/concourse"
	"golang.org/x/oauth2"
)

//go:generate counterfeiter . Client
type Client interface {
	ConcourseURL() string
	Builds(concourse.Page) ([]atc.Build, concourse.Pagination, error)
	BuildEvents(buildID string) ([]byte, error)
}

type client struct {
	concourseURL string
	username     string
	password     string
	team         string

	concourseCli concourse.Client
}

func NewClient(concourseURL, username, password, team string) *client {
	c := &client{
		concourseURL: concourseURL,
		username:     username,
		password:     password,
		team:         team,
	}
	return c
}

func (c *client) ConcourseURL() string {
	return c.concourseURL
}

func (c *client) Builds(page concourse.Page) ([]atc.Build, concourse.Pagination, error) {
	client, err := c.concourseClient()
	if err != nil {
		return []atc.Build{}, concourse.Pagination{}, err
	}
	return client.Builds(page)
}

func (c *client) BuildEvents(buildID string) ([]byte, error) {
	client, err := c.concourseClient()
	if err != nil {
		return []byte{}, err
	}
	events, err := client.BuildEvents(buildID)
	if err != nil {
		return []byte{}, err
	}
	defer events.Close()

	buf := bytes.NewBuffer([]byte{})
	var buildConfig event.TaskConfig
	for {
		ev, err := events.NextEvent()
		if err != nil {
			if err == io.EOF {
				return buf.Bytes(), nil
			} else {
				panic(fmt.Sprintf("failed to parse event - %s", err.Error()))
			}
		}

		switch e := ev.(type) {
		case event.Log:
			fmt.Fprintf(buf, "%s", e.Payload)

		case event.InitializeTask:
			buildConfig = e.TaskConfig

		case event.StartTask:
			argv := strings.Join(append([]string{buildConfig.Run.Path}, buildConfig.Run.Args...), " ")
			fmt.Fprintf(buf, "%s\n", argv)

		case event.Error:
			fmt.Fprintf(buf, "%s\n", e.Message)
		}
	}
}

func (c *client) concourseClient() (concourse.Client, error) {
	if c.concourseCli != nil {
		return c.concourseCli, nil
	}

	token, err := c.getAuthToken()
	if err != nil {
		return nil, fmt.Errorf("Failed to get token: %v", err)
	}

	transport := &oauth2.Transport{
		Source: oauth2.StaticTokenSource(token),
		Base:   transport(),
	}

	c.concourseCli = concourse.NewClient(c.concourseURL, &http.Client{Transport: transport}, false)
	return c.concourseCli, nil
}

func (c *client) getAuthToken() (*oauth2.Token, error) {
	target, err := rc.NewUnauthenticatedTarget(
		"concourse-flake-hunter",
		c.concourseURL,
		c.team,
		true,
		"",
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create target %s", err)
	}
	client := target.Client()

	oauth2Config := oauth2.Config{
		ClientID:     "fly",
		ClientSecret: "Zmx5",
		Endpoint:     oauth2.Endpoint{TokenURL: client.URL() + "/sky/token"},
		Scopes:       []string{"openid", "profile", "email", "federated:id", "groups"},
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client.HTTPClient())

	return oauth2Config.PasswordCredentialsToken(ctx, c.username, c.password)
}

func transport() http.RoundTripper {
	var transport http.RoundTripper

	transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
		Dial: (&net.Dialer{
			Timeout: 10 * time.Second,
		}).Dial,
		Proxy: http.ProxyFromEnvironment,
	}

	return transport
}
