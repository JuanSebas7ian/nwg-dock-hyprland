// Package gcal is Google Calendar for the Omarchy clock panel (Calendar API v3).
package gcal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"omarchy/internal/google"
)

const (
	API        = "https://www.googleapis.com/calendar/v3"
	Scopes     = "https://www.googleapis.com/auth/calendar.events https://www.googleapis.com/auth/calendar.calendarlist.readonly"
	SyncEvery  = 300
	PastDays   = 62
	FutureDays = 365
)

// Event is what the panel shows (same keys as before).
type Event struct {
	ID          string `json:"id"`
	CalendarID  string `json:"calendarId"`
	Calendar    string `json:"calendar"`
	Color       string `json:"color"`
	Summary     string `json:"summary"`
	AllDay      bool   `json:"allDay"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Link        string `json:"link"`
	Meet        string `json:"meet"`
	Recurring   bool   `json:"recurring"`
	SeriesID    string `json:"seriesId"`
	Status      string `json:"status"`
	Reminders   []int  `json:"reminders"`
	CanEdit     bool   `json:"canEdit"`
}

// Calendar is one entry of calendarList.
type Calendar struct {
	ID               string     `json:"id"`
	Summary          string     `json:"summary"`
	SummaryOverride  string     `json:"summaryOverride"`
	BackgroundColor  string     `json:"backgroundColor"`
	Primary          bool       `json:"primary"`
	AccessRole       string     `json:"accessRole"`
	Hidden           bool       `json:"hidden"`
	Selected         *bool      `json:"selected"`
	TimeZone         string     `json:"timeZone"`
	DefaultReminders []Reminder `json:"defaultReminders"`
}

type Reminder struct {
	Method  string `json:"method"`
	Minutes int    `json:"minutes"`
}

// RawEvent is the part of an API event that matters.
type RawEvent struct {
	ID               string `json:"id"`
	Summary          string `json:"summary"`
	Status           string `json:"status"`
	Location         string `json:"location"`
	Description      string `json:"description"`
	HTMLLink         string `json:"htmlLink"`
	HangoutLink      string `json:"hangoutLink"`
	RecurringEventID string `json:"recurringEventId"`
	Start            struct{ Date, DateTime string }
	End              struct{ Date, DateTime string }
	Reminders        struct {
		UseDefault *bool      `json:"useDefault"`
		Overrides  []Reminder `json:"overrides"`
	} `json:"reminders"`
}

func (c Calendar) name() string {
	if c.SummaryOverride != "" {
		return c.SummaryOverride
	}
	return c.Summary
}

func (c Calendar) color() string {
	if c.BackgroundColor != "" {
		return c.BackgroundColor
	}
	return "#888888"
}

func (c Calendar) shown() bool   { return !c.Hidden && (c.Selected == nil || *c.Selected) }
func (c Calendar) canEdit() bool { return c.AccessRole == "owner" || c.AccessRole == "writer" }

func popups(rs []Reminder) []int {
	out := []int{}
	for _, r := range rs {
		if r.Method == "popup" {
			out = append(out, r.Minutes)
		}
	}
	return out
}

// Slim keeps what the panel needs; reminders come from the calendar's
// defaults unless the event overrides them.
func Slim(ev RawEvent, cal Calendar) Event {
	desc := ev.Description
	if len([]rune(desc)) > 2000 {
		desc = string([]rune(desc)[:2000])
	}
	summary := ev.Summary
	if summary == "" {
		summary = "(no title)"
	}
	start, end := ev.Start.Date, ev.End.Date
	if start == "" {
		start = ev.Start.DateTime
	}
	if end == "" {
		end = ev.End.DateTime
	}
	rem := popups(cal.DefaultReminders)
	if ev.Reminders.UseDefault != nil && !*ev.Reminders.UseDefault {
		rem = popups(ev.Reminders.Overrides)
	}
	return Event{ID: ev.ID, CalendarID: cal.ID, Calendar: cal.name(), Color: cal.color(), Summary: summary, AllDay: ev.Start.Date != "",
		Start: start, End: end, Location: ev.Location, Description: desc, Link: ev.HTMLLink, Meet: ev.HangoutLink,
		Recurring: ev.RecurringEventID != "", SeriesID: ev.RecurringEventID, Status: ev.Status, Reminders: rem, CanEdit: cal.canEdit()}
}

// Form is what the panel sends to add an event.
type Form struct {
	CalendarID  string `json:"calendarId"`
	Summary     string `json:"summary"`
	Date        string `json:"date"`
	EndDate     string `json:"endDate"`
	Start       string `json:"start"`
	End         string `json:"end"`
	AllDay      bool   `json:"allDay"`
	Location    string `json:"location"`
	Description string `json:"description"`
	Reminder    any    `json:"reminder"`
}

func clock(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		t, err = time.Parse("15:04:05", s)
	}
	return t.Hour(), t.Minute(), err
}

// BuildEvent turns the form into an API body (local times; all-day end exclusive).
func BuildEvent(f Form, tz string) (map[string]any, error) {
	summary := strings.TrimSpace(f.Summary)
	if summary == "" {
		return nil, errors.New("the event needs a title")
	}
	date, err := time.Parse("2006-01-02", f.Date)
	if err != nil {
		return nil, fmt.Errorf("bad date %q", f.Date)
	}
	body := map[string]any{"summary": summary}
	if f.Location != "" {
		body["location"] = f.Location
	}
	if f.Description != "" {
		body["description"] = f.Description
	}
	if f.AllDay {
		endDate := date
		if f.EndDate != "" {
			if endDate, err = time.Parse("2006-01-02", f.EndDate); err != nil {
				return nil, fmt.Errorf("bad end date %q", f.EndDate)
			}
		}
		body["start"] = map[string]any{"date": date.Format("2006-01-02")}
		body["end"] = map[string]any{"date": endDate.AddDate(0, 0, 1).Format("2006-01-02")}
	} else {
		h, m, err := clock(f.Start)
		if err != nil {
			return nil, fmt.Errorf("bad start time %q", f.Start)
		}
		start := time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		if f.End != "" {
			eh, em, err := clock(f.End)
			if err != nil {
				return nil, fmt.Errorf("bad end time %q", f.End)
			}
			end = time.Date(date.Year(), date.Month(), date.Day(), eh, em, 0, 0, time.UTC)
		}
		if !end.After(start) {
			end = end.AddDate(0, 0, 1) // 23:00 -> 01:00 ends the next day
		}
		body["start"] = map[string]any{"dateTime": start.Format("2006-01-02T15:04:00"), "timeZone": tz}
		body["end"] = map[string]any{"dateTime": end.Format("2006-01-02T15:04:00"), "timeZone": tz}
	}
	if r := fmt.Sprint(f.Reminder); f.Reminder != nil && r != "" && r != "default" && r != "<nil>" {
		mins, err := strconv.Atoi(strings.TrimSuffix(r, ".0"))
		if err != nil {
			return nil, fmt.Errorf("bad reminder %q", r)
		}
		over := []any{}
		if mins >= 0 {
			over = append(over, map[string]any{"method": "popup", "minutes": mins})
		}
		body["reminders"] = map[string]any{"useDefault": false, "overrides": over}
	}
	return body, nil
}

// StartOf is when an event starts (all-day events remind at 09:00 local).
func StartOf(e Event) (time.Time, error) {
	if e.AllDay {
		d, err := time.ParseInLocation("2006-01-02", e.Start, time.Local)
		return d.Add(9 * time.Hour), err
	}
	return time.Parse(time.RFC3339, e.Start)
}

// Due is a reminder to show now.
type Due struct {
	Key     string
	Event   Event
	Minutes int
}

// DueReminders: every popup reminder whose time fell in the last 2 minutes, once.
func DueReminders(evs []Event, now time.Time, notified map[string]int64) []Due {
	var out []Due
	for _, e := range evs {
		start, err := StartOf(e)
		if err != nil {
			continue
		}
		for _, m := range e.Reminders {
			at := start.Add(-time.Duration(m) * time.Minute)
			key := fmt.Sprintf("%s|%s|%d", e.CalendarID, e.ID, m)
			if _, done := notified[key]; !done && !now.Before(at) && now.Before(at.Add(2*time.Minute)) {
				out = append(out, Due{key, e, m})
			}
		}
	}
	return out
}

// When is the "in 10 min" part of a reminder.
func When(m int) string {
	switch {
	case m == 0:
		return "now"
	case m < 60:
		return fmt.Sprintf("in %d min", m)
	case m < 1440:
		return fmt.Sprintf("in %d h", m/60)
	}
	return fmt.Sprintf("in %d day(s)", m/1440)
}

// LocalTZ is the IANA name of the system zone.
func LocalTZ() string {
	t, err := os.Readlink("/etc/localtime")
	if err != nil {
		return "UTC"
	}
	if _, after, ok := strings.Cut(t, "zoneinfo/"); ok {
		return after
	}
	return "UTC"
}

// Data is the cache file (~/.cache/omarchy-gcal/events.json).
type Data struct {
	TS        int64            `json:"ts"`
	Account   string           `json:"account"`
	TimeZone  string           `json:"timeZone"`
	Calendars []map[string]any `json:"calendars"`
	Events    []Event          `json:"events"`
}

// Service ties the auth and the files together.
type Service struct {
	Auth     *google.Auth
	Cache    string
	Notified string
}

func (s *Service) Configured() bool {
	_, e1 := os.Stat(s.Auth.ClientPath)
	_, e2 := os.Stat(s.Auth.TokenPath)
	return e1 == nil && e2 == nil
}

func esc(s string) string { return url.PathEscape(s) }

// Sync downloads calendars and events into the cache.
func (s *Service) Sync() (Data, error) {
	var cals []Calendar
	page := ""
	for {
		q := url.Values{"maxResults": {"250"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		var r struct {
			Items []Calendar `json:"items"`
			Next  string     `json:"nextPageToken"`
		}
		if err := s.Auth.API("GET", API+"/users/me/calendarList?"+q.Encode(), nil, &r); err != nil {
			return Data{}, err
		}
		cals = append(cals, r.Items...)
		if page = r.Next; page == "" {
			break
		}
	}
	now := time.Now().UTC()
	lo, hi := now.AddDate(0, 0, -PastDays).Format("2006-01-02T15:04:05Z"), now.AddDate(0, 0, FutureDays).Format("2006-01-02T15:04:05Z")
	events := []Event{}
	for _, cal := range cals {
		if !cal.shown() {
			continue
		}
		page := ""
		for {
			q := url.Values{"timeMin": {lo}, "timeMax": {hi}, "singleEvents": {"true"}, "orderBy": {"startTime"}, "maxResults": {"2500"}, "timeZone": {LocalTZ()}}
			if page != "" {
				q.Set("pageToken", page)
			}
			var r struct {
				Items []RawEvent `json:"items"`
				Next  string     `json:"nextPageToken"`
			}
			if err := s.Auth.API("GET", API+"/calendars/"+esc(cal.ID)+"/events?"+q.Encode(), nil, &r); err != nil {
				return Data{}, err
			}
			for _, ev := range r.Items {
				if ev.Status != "cancelled" {
					events = append(events, Slim(ev, cal))
				}
			}
			if page = r.Next; page == "" {
				break
			}
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Start != events[j].Start {
			return events[i].Start < events[j].Start
		}
		return events[i].Summary < events[j].Summary
	})
	d := Data{TS: time.Now().Unix(), TimeZone: LocalTZ(), Calendars: []map[string]any{}, Events: events}
	for _, c := range cals {
		if c.Primary {
			d.Account = c.ID
			if c.TimeZone != "" {
				d.TimeZone = c.TimeZone
			}
		}
		d.Calendars = append(d.Calendars, map[string]any{"id": c.ID, "summary": c.name(), "color": c.color(), "primary": c.Primary, "canEdit": c.canEdit(), "shown": c.shown()})
	}
	return d, google.WriteJSON(s.Cache, d, true)
}

// Events serves the cache, syncing first when it is older than maxAge (<0 = never).
func (s *Service) Events(maxAge int64) map[string]any {
	var d Data
	have := google.ReadJSON(s.Cache, &d)
	configured := s.Configured()
	errText := ""
	if configured && (!have || (maxAge >= 0 && time.Now().Unix()-d.TS > maxAge)) {
		nd, err := s.Sync()
		var ns *google.NeedsSetup
		switch {
		case errors.As(err, &ns):
			errText, configured = err.Error(), false
		case err != nil:
			errText = err.Error()
			if len(errText) > 200 {
				errText = errText[:200]
			}
		default:
			d, have = nd, true
		}
	}
	out := map[string]any{"ts": d.TS, "account": d.Account, "calendars": d.Calendars, "events": d.Events, "ok": true, "configured": configured, "error": errText}
	if !have {
		out["ts"], out["account"], out["calendars"], out["events"] = 0, "", []any{}, []any{}
	} else if d.TimeZone != "" {
		out["timeZone"] = d.TimeZone
	}
	if out["calendars"] == nil {
		out["calendars"] = []any{}
	}
	if out["events"] == nil {
		out["events"] = []any{}
	}
	return out
}

// Add creates an event and refreshes the cache.
func (s *Service) Add(spec string) (map[string]any, error) {
	var f Form
	if err := json.Unmarshal([]byte(spec), &f); err != nil {
		return nil, fmt.Errorf("bad event: %v", err)
	}
	cal := f.CalendarID
	if cal == "" {
		cal = "primary"
	}
	body, err := BuildEvent(f, LocalTZ())
	if err != nil {
		return nil, err
	}
	var ev struct{ ID, HTMLLink string }
	if err := s.Auth.API("POST", API+"/calendars/"+esc(cal)+"/events", body, &ev); err != nil {
		return nil, err
	}
	_, _ = s.Sync()
	return map[string]any{"ok": true, "id": ev.ID, "summary": body["summary"], "link": ev.HTMLLink}, nil
}

// Delete removes an event (or its whole series).
func (s *Service) Delete(cal, id string, series bool) error {
	if series {
		var ev struct {
			Rec string `json:"recurringEventId"`
		}
		if err := s.Auth.API("GET", API+"/calendars/"+esc(cal)+"/events/"+esc(id), nil, &ev); err == nil && ev.Rec != "" {
			id = ev.Rec
		}
	}
	if err := s.Auth.API("DELETE", API+"/calendars/"+esc(cal)+"/events/"+esc(id), nil, nil); err != nil {
		return err
	}
	_, _ = s.Sync()
	return nil
}

// Tick: sync every 5 min, desktop reminders every minute (run by the timer).
func (s *Service) Tick() {
	var d Data
	have := google.ReadJSON(s.Cache, &d)
	if s.Configured() && (!have || time.Now().Unix()-d.TS > SyncEvery) {
		if nd, err := s.Sync(); err == nil {
			d, have = nd, true
		}
	}
	if !have {
		return
	}
	notified := map[string]int64{}
	google.ReadJSON(s.Notified, &notified)
	for _, due := range DueReminders(d.Events, time.Now(), notified) {
		start, _ := StartOf(due.Event)
		body := start.Local().Format("15:04")
		if due.Event.AllDay {
			body = "All day"
		}
		if due.Event.Location != "" {
			body += " · " + due.Event.Location
		}
		_ = exec.Command("notify-send", "-a", "Google Calendar", "-i", "x-office-calendar", due.Event.Summary+" — "+When(due.Minutes), body).Run()
		notified[due.Key] = time.Now().Unix()
	}
	cut := time.Now().Unix() - 3*86400
	for k, v := range notified {
		if v <= cut {
			delete(notified, k)
		}
	}
	_ = google.WriteJSON(s.Notified, notified, true)
}
