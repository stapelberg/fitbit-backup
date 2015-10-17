# fitbit-backup

fitbit-backup retrieves weight data from fitbit. So, if you have a WiFi scale
like the [Fitbit Aria](https://www.fitbit.com/de/aria), you can make a backup
of the weight data that Fitbit stores for you. Of course, this tool is not
restricted to just the Fitbit Aria — the data of any product which uses Fitbit
to store weight data will be retrieved.

## Installation

To download and compile, use:

```bash
go get -u github.com/stapelberg/fitbit-backup
```

Afterwards, run `$GOPATH/bin/fitbit-backup` to make sure it works (see below
for configuration). Once you get it to spit out weight data, install a cronjob:

```
0 19 * * * /home/michael/gocode/bin/fitbit-backup -access_token_token=replace_this -access_token_secret=replace_this > ~/weight/fitbit-backup-$(date +'\%Y-\%m-\%d')
```

This cronjob will download weight data from Fitbit once a day and store it in a
separate file. That way, even if one download fails, you will not accidentally
overwrite your old backups, and you can even notice when Fitbit loses some data
for whichever reason (hasn’t happened yet for me, but who knows…).

## Configuration

In order to talk to fitbit, you first need to create an application. Go to
https://dev.fitbit.com/apps/new and fill in the form like in this example:

<img
src="https://github.com/stapelberg/fitbit-backup/raw/master/fitbit_app_registration.png"
width="800" alt="fitbit app registration screenshot">

Afterwards, fitbit will present you the “Client (Consumer) Secret” for the
newly created application. Specify that using the flag `-oauth2_client_secret`.

When running `fitbit-backup`, it will prompt you to visit a URL in your browser
in order to authorize the application to access your personal Fitbit account.
Point the flag `-oauth2_cache_path` to a writable file and `fitbit-backup` will
store the OAuth2 token in there. If you run `fitbit-backup` before the token
expires, it will be automatically renewed. Ideally, you only have to visit the
authorization URL in your browser once.

That’s it — now, when running `fitbit-backup` with all required flags, you
should get an output like this:

```
2013-07-30 18:00 65.9
2013-07-30 23:59 65.3
…
```
