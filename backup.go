package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/oauth2"
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

// The following code is intentionally very similar to
// camlistore.org/pkg/oauthutil, in the hope that it one day is included in the
// Go standard library…

// ErrNoAuthCode is returned when Token() has not found any valid cached token
// and TokenSource does not have an AuthCode for getting a new token.
var ErrNoAuthCode = errors.New("oauthutil: unspecified TokenSource.AuthCode")

type FileTokenSource struct {
	Config *oauth2.Config

	CacheFile string

	AuthCode func() string
}

var errExpiredToken = errors.New("expired token")

func cachedToken(cacheFile string) (*oauth2.Token, error) {
	tok := new(oauth2.Token)
	tokenData, err := ioutil.ReadFile(cacheFile)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tokenData, tok); err != nil {
		return nil, err
	}
	if !tok.Valid() {
		if tok != nil && time.Now().After(tok.Expiry) {
			return nil, errExpiredToken
		}
		return nil, errors.New("invalid token")
	}
	return tok, nil
}

func (src FileTokenSource) Token() (*oauth2.Token, error) {
	var tok *oauth2.Token
	var err error
	if src.CacheFile != "" {
		tok, err = cachedToken(src.CacheFile)
		if err == nil {
			return tok, nil
		}
		if err != errExpiredToken {
			log.Printf("Error getting token from %q: %v\n", src.CacheFile, err)
		}
	}
	if src.AuthCode == nil {
		return nil, ErrNoAuthCode
	}
	tok, err = src.Config.Exchange(oauth2.NoContext, src.AuthCode())
	if err != nil {
		return nil, fmt.Errorf("could not exchange auth code for a token: %v", err)
	}
	if src.CacheFile == "" {
		return tok, nil
	}
	tokenData, err := json.Marshal(&tok)
	if err != nil {
		return nil, fmt.Errorf("could not encode token as json: %v", err)
	}
	if err := ioutil.WriteFile(src.CacheFile, tokenData, 0600); err != nil {
		return nil, fmt.Errorf("could not cache token in %q: %v", src.CacheFile, err)
	}
	return tok, nil
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
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://www.fitbit.com/oauth2/authorize",
			TokenURL: "https://api.fitbit.com/oauth2/token",
		},
	}

	c := oauth2.NewClient(
		context.Background(),
		oauth2.ReuseTokenSource(nil, &FileTokenSource{
			Config:    conf,
			CacheFile: *cachePath,
			AuthCode: func() string {
				// Request an access token that expires in 30 days, so that we
				// have plenty of time to refresh it.
				authUrl := conf.AuthCodeURL("state", oauth2.AccessTypeOffline, oauth2.SetAuthURLParam("expires_in", "2592000"))

				fmt.Println("Get auth code from:")
				fmt.Println(authUrl)
				fmt.Println("Enter auth code (or entire URL):")
				sc := bufio.NewScanner(os.Stdin)
				sc.Scan()
				if u, err := url.Parse(sc.Text()); err == nil {
					if c := u.Query().Get("code"); c != "" {
						return c
					}
				}
				return strings.TrimSpace(sc.Text())
			},
		}))

	type weight struct {
		Bmi    float64 `json:"bmi"`
		Date   string  `json:"date"`
		Logid  uint    `json:"logid"`
		Time   string  `json:"time"`
		Weight float64 `json:"weight"`
	}

	type timeSeriesEntry struct {
		DateTime string `json:"dateTime"`
		Value    string `json:"value"`
	}

	type timeSeriesReply struct {
		Entries []timeSeriesEntry `json:"body-weight"`
	}

	type weightReply struct {
		Weights []weight `json:"weight"`
	}

	var timeseries timeSeriesReply
	var weights weightReply

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
	var err error
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
