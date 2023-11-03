package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/sheets/v4"
	"gopkg.in/yaml.v3"
)

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
	Credentials            string `yaml:"credentials"`
	SpreadsheetId          string `yaml:"spreadsheet_id"`
	ScheduledPaymentsSheet string `yaml:"scheduled_payments_sheet"`
	RecurringPaymentsSheet string `yaml:"recurring_payments_sheet"`
}

func ReadParamsFile(path string) (contents []byte, err error) {
	contents, err = ioutil.ReadFile(path)
	if err != nil {
		return
	}
	return
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

func main() {
	if len(os.Args[1:]) != 1 {
		log.Fatal("Please supply a single argument with the path to the config file")
	}

	paramsContent, err := ReadParamsFile(os.Args[1])
	if err != nil {
		log.Fatalf("Unable to read params file: %v", err)
	}

	params, err := ParseParams(paramsContent)
	if err != nil {
		log.Fatalf("Unable to parse params file: %v", err)
	}

	config, err := google.JWTConfigFromJSON([]byte(params.Credentials), sheets.SpreadsheetsScope)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	client := config.Client(oauth2.NoContext)
	svc, err := sheets.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve Sheets Client: %v", err)
	}

	// recurring payments
	sheet, err := getSheet(svc, params.SpreadsheetId, params.RecurringPaymentsSheet)
	if err != nil {
		log.Fatalf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.RecurringPaymentsSheet, err)
	}

	payments, err := readRecurringPayments(sheet)
	if err != nil {
		log.Fatalf("[%s/%s] failed to process recurring payments: %v", params.SpreadsheetId, params.RecurringPaymentsSheet, err)
	}
	fmt.Println("Pending recurring payments; cough it up man 💸 💸 💸")
	for _, p := range payments {
		fmt.Printf("  - %s\n", p.description)
	}

	// scheduled payments
	sheet, err = getSheet(svc, params.SpreadsheetId, params.ScheduledPaymentsSheet)
	if err != nil {
		log.Fatalf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.ScheduledPaymentsSheet, err)
	}

	payments, err = readScheduledPayments(sheet)
	if err != nil {
		log.Fatalf("[%s] failed to read sheet %s: %v", params.SpreadsheetId, params.ScheduledPaymentsSheet, err)
	}
	fmt.Println("Pending scheduled payments 💸 💸 💸")
	for _, p := range payments {
		fmt.Printf("  - %s\n", p.description)
	}

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
	// what's the current month?
	month := fmt.Sprintf("%d", time.Now().Month())
	// find index of current month
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
		return nil, errors.New("current month was not found in sheet header")
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
		if _, err = time.Parse(time.DateOnly, dueDate); err != nil {
			return nil, fmt.Errorf("failed to parse due date value %s: %v", dueDate, err)
		}
		payments = append(payments, NewPayment(row[descriptionIndex].(string)))
	}
	return payments, nil
}
