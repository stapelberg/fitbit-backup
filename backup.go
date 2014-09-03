package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/mrjones/oauth"
	"log"
	"net/http"
	"os"
	"time"
)

var (
	consumerKey = flag.String("oauth_consumer_key",
		"",
		"OAuth Client (Consumer) Key (see dev.fitbit.com)")
	consumerSecret = flag.String("oauth_consumer_secret",
		"",
		"OAuth Client (Consumer) Secret (see dev.fitbit.com)")
	accessTokenToken = flag.String("access_token_token",
		"",
		"OAuth AccessToken token part")
	accessTokenSecret = flag.String("access_token_secret",
		"",
		"OAuth AccessToken secret part")
)

func main() {
	flag.Parse()

	if *consumerKey == "" || *consumerSecret == "" {
		log.Fatal("-oauth_consumer_key or -oauth_consumer_secret not specified. Register an app at http://dev.fitbit.com to get these.")
	}

	c := oauth.NewConsumer(
		*consumerKey,
		*consumerSecret,
		oauth.ServiceProvider{
			RequestTokenUrl:   "https://api.fitbit.com/oauth/request_token",
			AuthorizeTokenUrl: "https://www.fitbit.com/oauth/authorize",
			AccessTokenUrl:    "https://api.fitbit.com/oauth/access_token",
		})

	c.AdditionalAuthorizationUrlParams = map[string]string{
		"application_name":   "fitbit-backup",
		"oauth_consumer_key": *consumerKey,
	}

	accessToken := &oauth.AccessToken{
		Token:  *accessTokenToken,
		Secret: *accessTokenSecret,
	}

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
			"https://api.fitbit.com/1/user/-/body/weight/date/today/max.json",
			map[string]string{},
			accessToken)
		tries += 1
		if err != nil {
			// Maybe the accessToken has expired?
			if tries == 1 && response.StatusCode == 401 {
				requestToken, url, err := c.GetRequestTokenAndUrl("oob")
				if err != nil {
					log.Fatal(err)
				}

				fmt.Println("(1) Go to: " + url)
				fmt.Println("(2) Grant access, you should get back a verification code.")
				fmt.Println("(3) Enter that verification code here: ")

				verificationCode := ""
				fmt.Scanln(&verificationCode)

				accessToken, err = c.AuthorizeToken(requestToken, verificationCode)
				if err != nil {
					log.Fatal(err)
				}

				log.Printf("Use -access_token_token=%s -access_token_secret=%s\n",
					accessToken.Token,
					accessToken.Secret)
			} else {
				log.Fatal(err)
			}
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
			requestUrl,
			map[string]string{},
			accessToken)
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
