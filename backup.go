package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/fitbit"
)

var (
	clientSecret = flag.String("oauth2_client_secret",
		"",
		"OAuth Client (Consumer) Secret (see dev.fitbit.com)")

	cachePath = flag.String("oauth2_cache_path",
		// No default value because expanding ~ is tricky.
		"",
		"Path to a JSON-encoded file which will contain the OAuth2 token")
)

var errExpiredToken = errors.New("expired token")

type cacherTransport struct {
	Base *oauth2.Transport
	Path string
}

func (c *cacherTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	cachedToken, err := tokenFromFile(c.Path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := c.Base.Source.Token(); err != nil {
		return nil, errExpiredToken
	}
	resp, err = c.Base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	newTok, err := c.Base.Source.Token()
	if err != nil {
		// While we’re unable to obtain a new token, the request was still
		// successful, so let’s gracefully handle this error by not caching a
		// new token. In either case, the user will need to re-authenticate.
		return resp, nil
	}
	if cachedToken == nil ||
		cachedToken.AccessToken != newTok.AccessToken ||
		cachedToken.RefreshToken != newTok.RefreshToken {
		bytes, err := json.Marshal(&newTok)
		if err != nil {
			return nil, err
		}
		if err := ioutil.WriteFile(c.Path, bytes, 0600); err != nil {
			return nil, err
		}
	}
	return resp, nil
}

func tokenFromFile(path string) (*oauth2.Token, error) {
	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t oauth2.Token
	if err := json.Unmarshal(content, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func authorize(conf *oauth2.Config) (*oauth2.Token, error) {
	tokens := make(chan *oauth2.Token)
	errors := make(chan error)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		code := r.FormValue("code")
		if code == "" {
			http.Error(w, "Missing 'code' parameter", http.StatusBadRequest)
			return
		}
		tok, err := conf.Exchange(oauth2.NoContext, code)
		if err != nil {
			errors <- fmt.Errorf("could not exchange auth code for a token: %v", err)
			return
		}
		tokens <- tok
	})
	go func() {
		// Unfortunately, we need to hard-code this port — when registering
		// with fitbit, full RedirectURLs need to be whitelisted (incl. port).
		errors <- http.ListenAndServe(":7319", nil)
	}()

	authUrl := conf.AuthCodeURL("state", oauth2.AccessTypeOffline)
	fmt.Println("Please visit the following URL to authorize:")
	fmt.Println(authUrl)
	select {
	case err := <-errors:
		return nil, err
	case token := <-tokens:
		return token, nil
	}
}

// Like oauth2.Config.Client(), but using cacherTransport to persist tokens.
func client(config *oauth2.Config, token *oauth2.Token) *http.Client {
	return &http.Client{
		Transport: &cacherTransport{
			Path: *cachePath,
			Base: &oauth2.Transport{
				Source: config.TokenSource(oauth2.NoContext, token),
			},
		},
	}
}

func main() {
	flag.Parse()

	if *clientSecret == "" {
		log.Fatal("-oauth2_client_secret not specified. Register an app at https://dev.fitbit.com to get one.")
	}

	conf := &oauth2.Config{
		ClientID:     "228XTZ",
		ClientSecret: *clientSecret,
		Scopes:       []string{"weight"},
		Endpoint:     fitbit.Endpoint,
		RedirectURL:  "http://localhost:7319/",
	}

	token, err := tokenFromFile(*cachePath)
	if err != nil && os.IsNotExist(err) {
		token, err = authorize(conf)
	}
	if err != nil {
		log.Fatal(err)
	}

	c := client(conf, token)

	// Send a request just to see if it errors. This quickly detects expired
	// tokens and allows us to re-authorize.
	if _, err := c.Get("https://api.fitbit.com/1/user/-/body/weight/date/today/max.json"); err != nil {
		if urlErr, ok := err.(*url.Error); !ok || urlErr.Err != errExpiredToken {
			log.Fatal(err)
		}
		if token, tokenErr := authorize(conf); tokenErr == nil {
			c = client(conf, token)
		} else {
			log.Fatalf("Request resulted in %v, trying to re-authorize resulted in %v", err, tokenErr)
		}
	}

	var timeseries struct {
		Entries []struct {
			DateTime string `json:"dateTime"`
			Value    string `json:"value"`
		} `json:"body-weight"`
	}

	var weights struct {
		Weights []struct {
			Bmi    float64 `json:"bmi"`
			Date   string  `json:"date"`
			Logid  uint    `json:"logid"`
			Time   string  `json:"time"`
			Weight float64 `json:"weight"`
		} `json:"weight"`
	}

	// The get-time-series reply lacks the time, it only contains the date.
	// Also, it returns one entry for each day of the month with the averaged
	// values instead of the raw measurement data.
	//
	// Therefore, we use get-body-weight repeatedly and just use
	// get-time-series to figure out when the first entry was recorded. We
	// cannot use the user’s registration date since she might backfill data
	// into the system using the API.
	tries := 0
	var response *http.Response
	for tries < 2 {
		response, err = c.Get(
			"https://api.fitbit.com/1/user/-/body/weight/date/today/max.json")
		tries += 1
		if err != nil {
			log.Fatal(err)
		}
	}

	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&timeseries); err != nil {
		log.Fatal(err)
	}

	// No entries recorded? Nothing to do.
	if len(timeseries.Entries) == 0 {
		log.Printf("The fitbit API returned no values.")
		os.Exit(0)
	}

	endDate, err := time.Parse("2006-01-02", timeseries.Entries[0].DateTime)
	if err != nil {
		log.Fatalf(`Could not parse timeseries date value "%s": %v`,
			timeseries.Entries[0].DateTime,
			err)
	}

	// Subtract 24 hours to make sure that no value is missed.
	endDate = endDate.Add(-24 * time.Hour)

	for endDate.Before(time.Now()) {
		endDate = endDate.Add(30 * 24 * time.Hour)
		requestUrl := fmt.Sprintf(
			"https://api.fitbit.com/1/user/-/body/log/weight/date/%s/30d.json",
			endDate.Format("2006-01-02"))

		response, err := c.Get(
			requestUrl)
		if err != nil {
			log.Fatal(err)
		}

		decoder := json.NewDecoder(response.Body)
		err = decoder.Decode(&weights)
		if err != nil {
			log.Fatal(err)
		}
		for _, entry := range weights.Weights {
			fmt.Printf("%s %s %.1f\n", entry.Date, entry.Time[0:5], entry.Weight)
		}
	}
}
