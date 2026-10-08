"""Tests for bin/omarchy-gcal without Google: event bodies, cache records and reminders.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_gcal
"""

import datetime as dt
import unittest
from importlib.machinery import SourceFileLoader
from importlib.util import module_from_spec, spec_from_loader
from pathlib import Path

PATH = Path(__file__).resolve().parent.parent / "bin" / "omarchy-gcal"
_loader = SourceFileLoader("omarchy_gcal", str(PATH))
gcal = module_from_spec(spec_from_loader(_loader.name, _loader))
_loader.exec_module(gcal)

CAL = {"id": "me@gmail.com", "summary": "Me", "backgroundColor": "#039be5", "accessRole": "owner",
       "defaultReminders": [{"method": "popup", "minutes": 10}, {"method": "email", "minutes": 60}]}


class Gcal(unittest.TestCase):
  def test_timed_event_uses_local_zone_and_default_hour(self):
    b = gcal.build_event({"summary": " Dentista ", "date": "2026-10-08", "start": "15:30"}, "America/Bogota")
    self.assertEqual(b["summary"], "Dentista")
    self.assertEqual(b["start"], {"dateTime": "2026-10-08T15:30:00", "timeZone": "America/Bogota"})
    self.assertEqual(b["end"]["dateTime"], "2026-10-08T16:30:00")
    self.assertNotIn("reminders", b)

  def test_end_before_start_rolls_to_next_day(self):
    b = gcal.build_event({"summary": "Turno", "date": "2026-10-08", "start": "23:00", "end": "01:00"}, "UTC")
    self.assertEqual(b["end"]["dateTime"], "2026-10-09T01:00:00")

  def test_all_day_end_is_exclusive(self):
    b = gcal.build_event({"summary": "Viaje", "date": "2026-10-08", "endDate": "2026-10-10", "allDay": True}, "UTC")
    self.assertEqual(b["start"], {"date": "2026-10-08"})
    self.assertEqual(b["end"], {"date": "2026-10-11"})

  def test_reminder_override_and_none(self):
    b = gcal.build_event({"summary": "a", "date": "2026-10-08", "start": "09:00", "reminder": "30"}, "UTC")
    self.assertEqual(b["reminders"], {"useDefault": False, "overrides": [{"method": "popup", "minutes": 30}]})
    b = gcal.build_event({"summary": "a", "date": "2026-10-08", "start": "09:00", "reminder": "-1"}, "UTC")
    self.assertEqual(b["reminders"], {"useDefault": False, "overrides": []})

  def test_title_required(self):
    with self.assertRaises(RuntimeError):
      gcal.build_event({"summary": "  ", "date": "2026-10-08", "start": "09:00"}, "UTC")

  def test_slim_keeps_popup_defaults_and_flags(self):
    ev = {"id": "e1", "summary": "Standup", "start": {"dateTime": "2026-10-08T09:00:00-05:00"},
          "end": {"dateTime": "2026-10-08T09:15:00-05:00"}, "reminders": {"useDefault": True},
          "recurringEventId": "s1", "hangoutLink": "https://meet.google.com/x"}
    s = gcal.slim(ev, CAL)
    self.assertEqual(s["reminders"], [10])
    self.assertTrue(s["recurring"] and s["canEdit"] and not s["allDay"])
    self.assertEqual(s["meet"], "https://meet.google.com/x")

  def test_due_reminders_fire_once_in_window(self):
    ev = gcal.slim({"id": "e1", "summary": "x", "start": {"dateTime": "2026-10-08T10:00:00+00:00"},
                    "end": {"dateTime": "2026-10-08T11:00:00+00:00"}, "reminders": {"useDefault": True}}, CAL)
    at = dt.datetime(2026, 10, 8, 9, 50, 30, tzinfo=dt.timezone.utc)
    due = gcal.due_reminders([ev], at, {})
    self.assertEqual([m for _, _, m in due], [10])
    self.assertEqual(gcal.due_reminders([ev], at, {due[0][0]: 1}), [])
    self.assertEqual(gcal.due_reminders([ev], at + dt.timedelta(minutes=5), {}), [])


if __name__ == "__main__":
  unittest.main()
