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

type Params struct {
	NotificationTopic      string `yaml:"ntfy_topic"`
	CronSchedule           string `yaml:"cron_schedule"`
	Credentials            string `yaml:"credentials"`
	SpreadsheetId          string `yaml:"spreadsheet_id"`
	ScheduledPaymentsSheet string `yaml:"scheduled_payments_sheet"`
	RecurringPaymentsSheet string `yaml:"recurring_payments_sheet"`
}

// parse the orkfile and populate the task inventory
func ParseParams(contents []byte) (*Params, error) {
	p := &Params{}
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
	return &Payment{
		description: description,
		due:         ToDate(time.Now().In(GreekTimeZone())),
	}
}

func (p *Payment) WithDueDate(due time.Time) *Payment {
	p.due = ToDate(due.In(GreekTimeZone()))
	return p
}

func (p *Payment) DiffFromNowInDays(now time.Time) int {
	now = ToDate(now.In(GreekTimeZone()))
	d := p.due.Sub(now).Hours() / 24
	return int(d)
}

func run(params *Params, config *jwt.Config) (string, error) {
	client := config.Client(oauth2.NoContext)
	svc, err := sheets.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return "", fmt.Errorf("Unable to retrieve Sheets Client: %v", err)
	}

	// recurring payments
	sheet, err := getSheet(svc, params.SpreadsheetId, params.RecurringPaymentsSheet)
	if err != nil {
		return "", fmt.Errorf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.RecurringPaymentsSheet, err)
	}

	recurring, err := readRecurringPayments(sheet)
	if err != nil {
		return "", fmt.Errorf("[%s/%s] failed to process recurring payments: %v", params.SpreadsheetId, params.RecurringPaymentsSheet, err)
	}

	// scheduled payments
	sheet, err = getSheet(svc, params.SpreadsheetId, params.ScheduledPaymentsSheet)
	if err != nil {
		return "", fmt.Errorf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.ScheduledPaymentsSheet, err)
	}

	scheduled, err := readScheduledPayments(sheet)
	if err != nil {
		return "", fmt.Errorf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.ScheduledPaymentsSheet, err)
	}

	// formulate payment report
	sections := []string{}
	if summary := SummarizeDelayedPayments(scheduled); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizePaymentsForToday(scheduled); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizePaymentsComingUp(scheduled); summary != "" {
		sections = append(sections, summary)
	}
	if summary := SummarizeMonthlyPayments(recurring); summary != "" {
		sections = append(sections, summary)
	}

	if len(sections) > 0 {
		return strings.Join(sections, "\n"), nil
	} else {
		return "🕶  Nothing to report", nil
	}
}

func notify(topic, report string) error {
	if err := SendNotification(topic, "💸 Payment Report", report, ""); err != nil {
		return fmt.Errorf("notification error: %v", err)
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

	params, err := ParseParams([]byte(configFileContents))
	if err != nil {
		log.Fatalf("Unable to parse params file: %v", err)
	}

	config, err := google.JWTConfigFromJSON([]byte(params.Credentials), sheets.SpreadsheetsScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	if cronMode {
		c := cron.New(cron.WithLocation(GreekTimeZone()))
		_, err := c.AddFunc(params.CronSchedule, func() {
			report, err := run(params, config)
			if err != nil {
				log.Printf("Failed to send payment report: %v", err)
			}
			if err := notify(params.NotificationTopic, report); err != nil {
				log.Printf("Failed to send error notification: %v", err)
			} else {
				log.Print("Report was sent")
			}
		})
		if err != nil {
			log.Fatalf("failed to setup cron: %v", err)
		}

		c.Start()

		log.Printf("started cron with schedule='%s'", params.CronSchedule)

		select {}
	} else {
		report, err := run(params, config)
		if err != nil {
			log.Printf("Failed to send payment report: %v", err)
		}
		if print {
			fmt.Print(report)
		}
		if err := notify(params.NotificationTopic, report); err != nil {
			log.Printf("Failed to send error notification: %v", err)
		}
	}
}

func SummarizeDelayedPayments(scheduled []*Payment) string {
	delayed := FindPaymentsUntil(scheduled, -1, time.Now())

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

func SummarizePaymentsForToday(scheduled []*Payment) string {
	payments := FindPaymentsAt(scheduled, 0, time.Now())

	if len(payments) > 0 {
		message := fmt.Sprintf("💸 Today: ")
		descriptions := []string{}
		for _, p := range payments {
			descriptions = append(descriptions, p.description)
		}
		return message + strings.Join(descriptions, ", ")
	}
	return "😎 Nothing for today"
}

func SummarizePaymentsComingUp(scheduled []*Payment) string {
	comingUp := []*Payment{}
	for _, p := range scheduled {
		d := p.DiffFromNowInDays(time.Now())
		if d == 1 || d == 2 {
			comingUp = append(comingUp, p)
		}
	}
	if len(comingUp) > 0 {
		message := fmt.Sprintf("⏳ Coming Up: ")
		descriptions := []string{}
		for _, p := range comingUp {
			d := p.DiffFromNowInDays(time.Now())
			if d == 1 || d == 2 {
				descriptions = append(descriptions, fmt.Sprintf("%s (%dd)", p.description, d))
			}
		}
		return message + strings.Join(descriptions, ", ")
	}
	return ""
}

func SummarizeMonthlyPayments(recurring []*Payment) string {
	if len(recurring) > 0 {
		return fmt.Sprintf("🗓 Monthly: %d pending", len(recurring))
	}
	return ""
}

func FindPaymentsAt(payments []*Payment, diff int, now time.Time) []*Payment {
	found := []*Payment{}
	for _, p := range payments {
		if p.DiffFromNowInDays(now) == diff {
			found = append(found, p)
		}
	}
	return found
}

func FindPaymentsUntil(payments []*Payment, maxDiff int, now time.Time) []*Payment {
	delayed := []*Payment{}
	for _, p := range payments {
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

func readRecurringPayments(rows [][]interface{}) ([]*Payment, error) {
	// what's the previous month?
	month := fmt.Sprintf("%d", time.Now().AddDate(0, -1, 0).Month())
	// find index of previous month
	descriptionIndex := -1
	columnIndex := -1
	for idx, v := range rows[0] {
		val := v.(string)
		if val == "Description" {
			descriptionIndex = idx
		}
		if val == string(month) {
			columnIndex = idx
		}
	}
	if columnIndex == -1 {
		return nil, errors.New("previous month was not found in sheet header")
	}
	if descriptionIndex == -1 {
		return nil, errors.New("description label was not found in sheet header")
	}

	payments := []*Payment{}

	for _, row := range rows[1:] {
		// `len(row) <= columnIndex` means that there are values in the row before the
		// value column of interest -- so for sure, this month is pending
		if len(row) <= columnIndex || row[columnIndex].(string) == "" {
			payments = append(payments, NewPayment(row[descriptionIndex].(string)))
		}
	}
	return payments, nil
}

func readScheduledPayments(rows [][]interface{}) ([]*Payment, error) {
	// find index of current month
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
	if dueDateIndex == -1 {
		return nil, errors.New("due date was not found in sheet header")
	}
	if descriptionIndex == -1 {
		return nil, errors.New("description label was not found in sheet header")
	}
	if paymentDateIndex == -1 {
		return nil, errors.New("payment date was not found in sheet header")
	}

	payments := []*Payment{}
	var (
		err error
		due time.Time
	)

	for _, row := range rows[1:] {
		dueDate := row[dueDateIndex].(string)
		paymentDate := row[paymentDateIndex].(string)
		if paymentDate != "" {
			continue
		}
		if dueDate == "" {
			payments = append(payments, NewPayment(row[descriptionIndex].(string)))
			continue
		}
		if due, err = time.Parse(time.DateOnly, dueDate); err != nil {
			return nil, fmt.Errorf("failed to parse due date value %s: %v", dueDate, err)
		}
		payments = append(payments, NewPayment(row[descriptionIndex].(string)).WithDueDate(due))
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
