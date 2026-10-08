import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// Under the month grid: the picked day's events and the ways to the other
// screens; or how to connect Google Calendar.
Column {
  id: day
  property var cal: null
  spacing: Style.space(6)

  readonly property bool ready: !!cal && cal.gcConfigured
  readonly property var evs: cal ? cal.dayEvents : []

  Notice { visible: !!day.cal && day.cal.message !== ""; text: day.cal ? day.cal.message : ""; level: day.cal && day.cal.messageIsError ? "crit" : "info" }

  // ---- not connected
  Column {
    visible: !!day.cal && !!day.cal.gcData && !day.ready
    width: parent.width
    spacing: Style.space(6)
    Section { title: "GOOGLE CALENDAR" }
    Line {
      small: true
      tone: day.cal && day.cal.gcData && day.cal.gcData.error ? "urgent" : "dim"
      text: day.cal && day.cal.gcData && day.cal.gcData.error ? day.cal.gcData.error
        : "Not connected yet. Connect once to see your events here, add new ones and delete them."
    }
    ActionRow {
      icon: "󰃭"
      label: "Connect Google Calendar"
      hint: "Opens a terminal: your OAuth client (5 min, once) and the Google sign-in"
      onActivated: day.cal.setupGoogle()
    }
  }

  // ---- the picked day
  Column {
    visible: day.ready
    width: parent.width
    spacing: Style.space(4)
    Section { title: day.cal ? day.cal.dayTitle(day.cal.selectedKey).toUpperCase() + (day.evs.length ? " · " + day.evs.length : "") : "" }
    Line {
      visible: day.evs.length === 0
      small: true
      tone: "dim"
      text: "Nothing scheduled. Double-click a day or press n to add an event."
    }
    Repeater {
      model: day.evs
      EventRow {
        required property var modelData
        ev: modelData
        cal: day.cal
        onPicked: day.cal.showEvent(modelData)
      }
    }
    RowLayout {
      width: parent.width
      spacing: Style.space(6)
      Button {
        Layout.fillWidth: true
        text: "New event"
        iconText: "󰐕"
        bordered: true
        selected: true
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        fontSize: Style.font.caption
        onClicked: day.cal.go("new")
      }
      Button {
        Layout.fillWidth: true
        text: "Agenda"
        iconText: "󰃮"
        bordered: true
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        fontSize: Style.font.caption
        onClicked: day.cal.go("agenda")
      }
      Button {
        Layout.fillWidth: true
        text: "Google"
        iconText: "󰖟"
        bordered: true
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        fontSize: Style.font.caption
        onClicked: day.cal.openUrl("https://calendar.google.com/calendar/r/day/" + day.cal.selectedKey.replace(/-/g, "/"))
      }
    }
    Caption {
      text: day.cal && day.cal.gcData ? (day.cal.gcData.account || "") + " · synced " + Theme.ago(day.cal.gcData.ts) + (day.cal.gcData.error ? " · offline" : "") + " · keys: n new · a agenda · r sync" : ""
    }
  }
}
