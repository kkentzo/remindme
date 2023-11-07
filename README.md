Process:

1. Create new project (remindme-app) in google console
2. API & Services: Enable Google Sheets API
3. Create Service Account for Google Sheets API
4. Generate key for service account (this will download a JSON file)
5. Share the google sheet with the service account's email

TODO:

- [x] run as cron or on-demand (cmd-line switch)
- [x] deploy cron
- [x] cron string in config.yml
- [ ] error notifications
- [ ] API (for health check, reporting, running ad-hoc)
- [ ] extract google sheets as a service
