package gcal

import (
	"reflect"
	"testing"
	"time"

	"omarchy/internal/google"
)

var cal = Calendar{ID: "me@gmail.com", Summary: "Me", BackgroundColor: "#039be5", AccessRole: "owner",
	DefaultReminders: []Reminder{{"popup", 10}, {"email", 60}}}

func TestTimedEventUsesLocalZoneAndDefaultHour(t *testing.T) {
	b, err := BuildEvent(Form{Summary: " Dentista ", Date: "2026-10-08", Start: "15:30"}, "America/Bogota")
	if err != nil || b["summary"] != "Dentista" || !reflect.DeepEqual(b["start"], map[string]any{"dateTime": "2026-10-08T15:30:00", "timeZone": "America/Bogota"}) ||
		b["end"].(map[string]any)["dateTime"] != "2026-10-08T16:30:00" || b["reminders"] != nil {
		t.Fatalf("%v %v", b, err)
	}
}

func TestEndBeforeStartRollsAndAllDayEndIsExclusive(t *testing.T) {
	b, _ := BuildEvent(Form{Summary: "Turno", Date: "2026-10-08", Start: "23:00", End: "01:00"}, "UTC")
	if b["end"].(map[string]any)["dateTime"] != "2026-10-09T01:00:00" {
		t.Fatal(b)
	}
	b, _ = BuildEvent(Form{Summary: "Viaje", Date: "2026-10-08", EndDate: "2026-10-10", AllDay: true}, "UTC")
	if !reflect.DeepEqual(b["start"], map[string]any{"date": "2026-10-08"}) || !reflect.DeepEqual(b["end"], map[string]any{"date": "2026-10-11"}) {
		t.Fatal(b)
	}
}

func TestReminderOverrideNoneAndTitleRequired(t *testing.T) {
	b, _ := BuildEvent(Form{Summary: "a", Date: "2026-10-08", Start: "09:00", Reminder: "30"}, "UTC")
	if !reflect.DeepEqual(b["reminders"], map[string]any{"useDefault": false, "overrides": []any{map[string]any{"method": "popup", "minutes": 30}}}) {
		t.Fatal(b["reminders"])
	}
	b, _ = BuildEvent(Form{Summary: "a", Date: "2026-10-08", Start: "09:00", Reminder: -1.0}, "UTC")
	if !reflect.DeepEqual(b["reminders"], map[string]any{"useDefault": false, "overrides": []any{}}) {
		t.Fatal(b["reminders"])
	}
	b, _ = BuildEvent(Form{Summary: "a", Date: "2026-10-08", Start: "09:00", Reminder: "default"}, "UTC")
	if b["reminders"] != nil {
		t.Fatal("default must keep the calendar's reminders")
	}
	if _, err := BuildEvent(Form{Summary: "  ", Date: "2026-10-08", Start: "09:00"}, "UTC"); err == nil {
		t.Fatal("empty title accepted")
	}
	if _, err := BuildEvent(Form{Summary: "x", Date: "08/10/2026"}, "UTC"); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestSecretPastedTwiceOrWithSpaces(t *testing.T) {
	if google.CleanSecret("GOCSPX-abcGOCSPX-abcGOCSPX-abc") != "GOCSPX-abc" || google.CleanSecret(" GOCSPX-abc \n") != "GOCSPX-abc" ||
		google.CleanSecret("GOCSPX-abcGOCSPX-xyz") != "GOCSPX-abcGOCSPX-xyz" {
		t.Fatal("CleanSecret")
	}
}

func TestSlimKeepsPopupDefaultsAndFlags(t *testing.T) {
	var ev RawEvent
	ev.ID, ev.Summary, ev.RecurringEventID, ev.HangoutLink = "e1", "Standup", "s1", "https://meet.google.com/x"
	ev.Start.DateTime, ev.End.DateTime = "2026-10-08T09:00:00-05:00", "2026-10-08T09:15:00-05:00"
	s := Slim(ev, cal)
	if !reflect.DeepEqual(s.Reminders, []int{10}) || !s.Recurring || !s.CanEdit || s.AllDay || s.Meet != "https://meet.google.com/x" || s.Color != "#039be5" {
		t.Fatalf("%+v", s)
	}
	f := false
	ev.Reminders.UseDefault = &f
	ev.Reminders.Overrides = []Reminder{{"popup", 5}}
	ev.Summary = ""
	if s := Slim(ev, cal); !reflect.DeepEqual(s.Reminders, []int{5}) || s.Summary != "(no title)" {
		t.Fatalf("%+v", s)
	}
}

func TestDueRemindersFireOnceInWindow(t *testing.T) {
	var ev RawEvent
	ev.ID, ev.Summary = "e1", "x"
	ev.Start.DateTime, ev.End.DateTime = "2026-10-08T10:00:00+00:00", "2026-10-08T11:00:00+00:00"
	e := Slim(ev, cal)
	at := time.Date(2026, 10, 8, 9, 50, 30, 0, time.UTC)
	due := DueReminders([]Event{e}, at, map[string]int64{})
	if len(due) != 1 || due[0].Minutes != 10 {
		t.Fatalf("%+v", due)
	}
	if len(DueReminders([]Event{e}, at, map[string]int64{due[0].Key: 1})) != 0 || len(DueReminders([]Event{e}, at.Add(5*time.Minute), map[string]int64{})) != 0 {
		t.Fatal("must fire once, inside its 2-minute window")
	}
	if When(0) != "now" || When(15) != "in 15 min" || When(120) != "in 2 h" || When(2880) != "in 2 day(s)" {
		t.Fatal("When")
	}
}
