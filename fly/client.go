package fly

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/event"
	"github.com/concourse/concourse/fly/rc"
	"github.com/concourse/concourse/go-concourse/concourse"
	"golang.org/x/oauth2"
)

const (
	targetName = "concourse-flake-hunter"
)

type Client interface {
	InitConcourseClient() error
	InvalidateConcourseClient() error
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

	isInSlowTest := false

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
			if isInSlowTest {
				if strings.Contains(e.Payload, "------------------------------") {
					isInSlowTest = false
				}
				continue
			}

			if strings.Contains(e.Payload, "SLOW TEST") {
				isInSlowTest = true
				continue
			}

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

func (c *client) InitConcourseClient() error {
	token, err := c.getAuthToken()
	if err != nil {
		return fmt.Errorf("Failed to get token: %v", err)
	}

	transport := &oauth2.Transport{
		Source: oauth2.StaticTokenSource(token),
		Base:   transport(),
	}

	c.concourseCli = concourse.NewClient(c.concourseURL, &http.Client{Transport: transport}, false)
	if _, err = c.concourseCli.GetInfo(); err != nil && err.Error() == "not authorized" {
		c.InvalidateConcourseClient()
		c.InitConcourseClient()
	}
	return nil
}

func (c *client) InvalidateConcourseClient() error {
	c.concourseCli = nil
	return rc.DeleteTarget(targetName)
}

func (c *client) concourseClient() (concourse.Client, error) {
	if c.concourseCli != nil {
		return c.concourseCli, nil
	}

	if err := c.InitConcourseClient(); err != nil {
		return nil, err
	}
	return c.concourseCli, nil
}

func (c *client) getAuthToken() (token *oauth2.Token, err error) {
	var target rc.Target
	target, err = rc.LoadUnauthenticatedTarget(
		targetName,
		c.team,
		true,
		"",
		"",
		"",
		false,
	)
	if err == nil {
		return &oauth2.Token{TokenType: target.Token().Type, AccessToken: target.Token().Value}, nil
	}

	target, err = rc.NewUnauthenticatedTarget(
		targetName,
		c.concourseURL,
		c.team,
		true,
		"",
		"",
		"",
		false,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create target %s", err)
	}
	client := target.Client()

	defer func() {
		rc.SaveTarget(
			targetName,
			c.concourseURL,
			true,
			c.team,
			&rc.TargetToken{
				Type:  token.TokenType,
				Value: token.AccessToken,
			},
			"",
			"",
			"",
		)
	}()

	if c.username != "" && c.password != "" {
		return c.passwordGrant(client)
	}

	return c.authCodeGrant(client.URL())
}

func (c *client) passwordGrant(client concourse.Client) (*oauth2.Token, error) {
	oauth2Config := oauth2.Config{
		ClientID:     "fly",
		ClientSecret: "Zmx5",
		Endpoint:     oauth2.Endpoint{TokenURL: client.URL() + "/sky/token"},
		Scopes:       []string{"openid", "profile", "email", "federated:id", "groups"},
	}

	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, client.HTTPClient())

	return oauth2Config.PasswordCredentialsToken(ctx, c.username, c.password)
}

func (c *client) authCodeGrant(targetURL string) (*oauth2.Token, error) {
	var tokenStr string

	tokenChannel := make(chan string)
	errorChannel := make(chan error)

	go readTokenFromInputStream(tokenChannel, errorChannel)

	portChannel := make(chan string)

	go listenForTokenCallback(tokenChannel, errorChannel, portChannel, targetURL)

	port := <-portChannel

	var openURL string

	fmt.Println("Please, navigate to the following URL in your browser to get authorized:")
	fmt.Println("")

	openURL = fmt.Sprintf("%s/login?fly_port=%s", targetURL, port)

	fmt.Printf("  %s\n", openURL)

	fmt.Println("\nor paste your token here:")

	select {
	case tokenStrMsg := <-tokenChannel:
		tokenStr = tokenStrMsg
	case err := <-errorChannel:
		return nil, err
	}

	segments := strings.SplitN(tokenStr, " ", 2)

	return &oauth2.Token{TokenType: segments[0], AccessToken: segments[1]}, nil
}

func readTokenFromInputStream(tokenChannel chan string, errorChannel chan error) {
	tokenBytes, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		errorChannel <- err
		return
	}
	tokenChannel <- string(tokenBytes)
}

func listenForTokenCallback(tokenChannel chan string, errorChannel chan error, portChannel chan string, targetURL string) {
	s := &http.Server{
		Addr: "127.0.0.1:0",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", targetURL)
			tokenChannel <- r.FormValue("token")
			if r.Header.Get("Upgrade-Insecure-Requests") != "" {
				http.Redirect(w, r, fmt.Sprintf("%s/fly_success?noop=true", targetURL), http.StatusFound)
			}
		}),
	}

	err := listenAndServeWithPort(s, portChannel)
	if err != nil {
		errorChannel <- err
	}
}

func listenAndServeWithPort(srv *http.Server, portChannel chan string) error {
	addr := srv.Addr
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return err
	}

	portChannel <- port

	return srv.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)})
}

type tcpKeepAliveListener struct {
	*net.TCPListener
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
