# remindme

## Introduction

This program runs a cron job that:

- looks up one or more google spreadsheets
- for every spreadsheet, it figures out the pending or completed
  payments (based on column names)
- assembles a payment report
- sends the payment report as a push notification to a ntfy.sh topic

Some program details can be specified in a config file that is built
into the application (see `config.sample.yml` as an example).

## Google API Integration

1. Create new project in google cloud console
2. API & Services: Enable Google Sheets API
3. Create Service Account for Google Sheets API
4. Generate key for service account (this will download a JSON file
   which needs to be included in the config file)
5. Share the google sheet with the service account's email

## Run Locally

The application can be built locally by running;

```bash
$ ork build
```

Grab `ork` from [here](https://github.com/kkentzo/ork/releases/latest).

The above action will produce the executable `bin/remindme`. Run
`./bin/remindme -h` for options.

## Deployment

The program can be deployed to `fly.io` by running `flyctl deploy`
(having set up the fly.io credentials beforehand).

## TODO

- [x] run as cron or on-demand (cmd-line switch)
- [x] deploy cron
- [x] cron string in config.yml
- [ ] error notifications
- [ ] API (for health check, reporting, running ad-hoc)
- [ ] extract google sheets as a service
