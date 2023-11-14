package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	"google.golang.org/api/option"
	"google.golang.org/api/sheets/v4"
	"gopkg.in/yaml.v3"
)

//go:embed config.yml
var configFileContents string

var _GR *time.Location

func GreekTimeZone() *time.Location {
	if _GR == nil {
		loc, err := time.LoadLocation("Europe/Athens")
		if err != nil {
			log.Fatalf("error loading location: %v", err)
		}
		_GR = loc
	}
	return _GR
}

func ToDate(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, GreekTimeZone())
}

type Sheet struct {
	SpreadsheetId string `yaml:"spreadsheet_id"`
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
}

type Config struct {
	NotificationTopic string   `yaml:"ntfy_topic"`
	CronSchedule      string   `yaml:"cron_schedule"`
	Credentials       string   `yaml:"credentials"`
	Sheets            []*Sheet `yaml:"sheets"`
}

// parse the orkfile and populate the task inventory
func ParseConfig(contents []byte) (*Config, error) {
	p := &Config{}
	if err := yaml.Unmarshal(contents, p); err != nil {
		return nil, err
	}
	return p, nil
}

type Payment struct {
	description string
	due         time.Time
}

func NewPayment(description string) *Payment {
	return &Payment{description: description}
}

func (p *Payment) WithDueDate(due time.Time) *Payment {
	p.due = ToDate(due.In(GreekTimeZone()))
	return p
}

func (p *Payment) IsDue() bool {
	return p.due != time.Time{}
}

func (p *Payment) DiffFromNowInDays(now time.Time) int {
	now = ToDate(now.In(GreekTimeZone()))
	d := p.due.Sub(now).Hours() / 24
	return int(d)
}

func run(config *Config, jwtcfg *jwt.Config, print bool) error {
	client := jwtcfg.Client(oauth2.NoContext)
	svc, err := sheets.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("Unable to retrieve Sheets Client: %v", err)
	}

	payments := []*Payment{}

	for _, sheet := range config.Sheets {
		rows, err := getSheet(svc, sheet.SpreadsheetId, sheet.Name)
		if err != nil {
			return fmt.Errorf("failed to read sheet %s: %v", sheet.Name, err)
		}
		p, err := readPayments(rows)
		if err != nil {
			return fmt.Errorf("failed to read payments from sheet '%s': %v", sheet.Name, err)
		}
		payments = append(payments, p...)
	}

	// formulate payment report
	sections := []string{}
	if summary := SummarizeDelayedPayments(payments); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizePaymentsForToday(payments); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizePaymentsComingUp(payments, 2); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizeTotalPayments(payments, 30); summary != "" {
		sections = append(sections, summary)
	}
	if len(sections) == 0 {
		sections = append(sections, "🕶  Nothing to report")
	}

	// format and send report
	report := strings.Join(sections, "\n")

	if print {
		fmt.Print(report)
	}

	if err := SendNotification(config.NotificationTopic, "Payment Report", report, ""); err != nil {
		return fmt.Errorf("failed to send notification: %v", err)
	}
	return nil
}

func main() {
	var (
		print    bool
		cronMode bool
	)
	flag.BoolVar(&print, "print", false, "Print the report on screen as well")
	flag.BoolVar(&cronMode, "cron", true, "Enable/disable cron mode")
	flag.Parse()

	log.Printf("cron_mode=%v", cronMode)

	config, err := ParseConfig([]byte(configFileContents))
	if err != nil {
		log.Fatalf("Unable to parse config file: %v", err)
	}

	log.Printf("Found %d sheets", len(config.Sheets))

	jwtcfg, err := google.JWTConfigFromJSON([]byte(config.Credentials), sheets.SpreadsheetsScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	if cronMode {
		c := cron.New(cron.WithLocation(GreekTimeZone()))
		_, err := c.AddFunc(config.CronSchedule, func() {
			if err := run(config, jwtcfg, print); err != nil {
				log.Printf(err.Error())
			}
		})

		if err != nil {
			log.Fatalf("failed to setup cron: %v", err)
		}

		c.Start()

		log.Printf("started cron with schedule='%s'", config.CronSchedule)

		select {}
	} else {
		if err := run(config, jwtcfg, print); err != nil {
			log.Printf(err.Error())
		}
	}
}

func SummarizeDelayedPayments(payments []*Payment) string {
	delayed := FindPaymentsUntil(payments, -1, time.Now())

	if len(delayed) > 0 {
		message := fmt.Sprintf("⚠ Delayed: ")
		descriptions := []string{}
		for _, p := range delayed {
			descriptions = append(descriptions, p.description)
		}
		return message + strings.Join(descriptions, ", ")
	}
	return ""
}

func SummarizePaymentsForToday(payments []*Payment) string {
	scheduled := FindPaymentsAt(payments, 0, time.Now())

	if len(scheduled) > 0 {
		message := fmt.Sprintf("💸 Today: ")
		descriptions := []string{}
		for _, p := range scheduled {
			descriptions = append(descriptions, p.description)
		}
		return message + strings.Join(descriptions, ", ")
	}
	return "😎 Nothing for today"
}

func SummarizePaymentsComingUp(payments []*Payment, timeWindowInDays int) string {
	comingUp := []*Payment{}
	for _, p := range payments {
		// skip non-due payments
		if !p.IsDue() {
			continue
		}
		d := p.DiffFromNowInDays(time.Now())
		if d >= 1 && d <= timeWindowInDays {
			comingUp = append(comingUp, p)
		}
	}
	if len(comingUp) > 0 {
		message := fmt.Sprintf("⏳ Coming Up (next %d days): ", timeWindowInDays)
		descriptions := []string{}
		for _, p := range comingUp {
			descriptions = append(descriptions, fmt.Sprintf("%s", p.description))
		}
		return message + strings.Join(descriptions, ", ")
	}
	return fmt.Sprintf("😎 Nothing coming up (next %d days)", timeWindowInDays)
}

func SummarizeTotalPayments(payments []*Payment, timeWindowInDays int) string {
	n := 0
	for _, p := range payments {
		if p.DiffFromNowInDays(time.Now()) <= timeWindowInDays {
			n += 1
		}
	}
	return fmt.Sprintf("💰 Total %d payments pending during the next %d days", n, timeWindowInDays)
}

func FindPaymentsAt(payments []*Payment, diff int, now time.Time) []*Payment {
	found := []*Payment{}
	for _, p := range payments {
		// skip non-due payments
		if !p.IsDue() {
			continue
		}
		if p.DiffFromNowInDays(now) == diff {
			found = append(found, p)
		}
	}
	return found
}

func FindPaymentsUntil(payments []*Payment, maxDiff int, now time.Time) []*Payment {
	delayed := []*Payment{}
	for _, p := range payments {
		// skip non-due payments
		if !p.IsDue() {
			continue
		}
		if p.DiffFromNowInDays(now) <= maxDiff {
			delayed = append(delayed, p)
		}
	}
	return delayed
}

func getSheet(svc *sheets.Service, spreadsheetId, sheetName string) ([][]interface{}, error) {
	res, err := svc.Spreadsheets.Values.Get(spreadsheetId, sheetName).Do()
	if err != nil {
		return nil, err
	}
	rows := res.Values
	if len(rows) <= 1 {
		return nil, errors.New("no data found")
	}
	return rows, nil

}

func readPayments(rows [][]interface{}) ([]*Payment, error) {
	descriptionIndex := -1
	dueDateIndex := -1
	paymentDateIndex := -1
	for idx, v := range rows[0] {
		val := v.(string)
		if val == "Description" {
			descriptionIndex = idx
		}
		if val == "Due Date" {
			dueDateIndex = idx
		}
		if val == "Payment Date" {
			paymentDateIndex = idx
		}
	}
	if descriptionIndex == -1 {
		return nil, errors.New("description label was not found in sheet header")
	}
	if paymentDateIndex == -1 {
		return nil, errors.New("payment date was not found in sheet header")
	}

	payments := []*Payment{}
	var (
		err     error
		due     time.Time
		dueDate string
	)

	for idx, row := range rows[1:] {
		if descriptionIndex > len(row)-1 {
			return nil, fmt.Errorf("can not read description (column=%d) in row %d", descriptionIndex, idx)
		}
		if dueDateIndex > len(row)-1 {
			return nil, fmt.Errorf("can not read due date (column=%d) in row %d", dueDateIndex, idx)
		}
		if paymentDateIndex > len(row)-1 {
			return nil, fmt.Errorf("can not read payment date (column=%d) in row %d", paymentDateIndex, idx)
		}

		description := row[descriptionIndex].(string)

		if dueDateIndex >= 0 {
			dueDate = row[dueDateIndex].(string)
		}
		paymentDate := row[paymentDateIndex].(string)
		if paymentDate != "" {
			// already paid -- skip
			continue
		}
		if dueDateIndex == -1 {
			// not a scheduled payment -- add to payments and continue
			payments = append(payments, NewPayment(description))
			continue
		}
		// scheduled payment -- parse due date
		if due, err = time.Parse(time.DateOnly, dueDate); err != nil {
			return nil, fmt.Errorf("failed to parse due date value %s: %v", dueDate, err)
		}
		payments = append(payments, NewPayment(description).WithDueDate(due))
	}
	return payments, nil
}

func SendNotification(topic, title, message, tag string) error {
	host := fmt.Sprintf("https://ntfy.sh/%s", topic)
	req, err := http.NewRequest(http.MethodPost, host, strings.NewReader(message))
	if err != nil {
		return fmt.Errorf("failed to create http request: %v", err)
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", tag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("error sending http request: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("server responded with status=%d", res.StatusCode)
	}
	return nil
}
