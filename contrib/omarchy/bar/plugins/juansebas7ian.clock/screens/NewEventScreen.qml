import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../Model.js" as Model

// New event: title, day (picked on the grid), time or all day, calendar,
// reminder, place and notes. Enter in the title saves.
Column {
  id: ne
  property var cal: null
  spacing: Style.space(8)

  property bool allDay: false
  property string calendarId: ""
  property string reminder: "default"
  property string error: ""

  readonly property var writable: cal && cal.gcData ? cal.gcData.calendars.filter(function(c) { return c.canEdit }) : []
  readonly property var chosen: writable.filter(function(c) { return c.id === ne.calendarId })[0] || writable.filter(function(c) { return c.primary })[0] || writable[0] || null

  function pad(n) { return (n < 10 ? "0" : "") + n }
  function defaultStart() {
    var now = new Date()
    if (cal && cal.selectedKey === Model.keyForDate(now)) return pad(Math.min(23, now.getHours() + 1)) + ":00"
    return "09:00"
  }
  function plusHour(t) {
    var m = /^(\d{1,2}):(\d{2})$/.exec(t)
    if (!m) return ""
    return pad((+m[1] + 1) % 24) + ":" + m[2]
  }
  function shiftDate(days) {
    var d = new Date()
    d.setDate(d.getDate() + days)
    dateField.text = Model.keyForDate(d)
  }
  function save() {
    error = ""
    if (titleField.text.trim() === "") { error = "Give it a title."; titleField.forceActiveFocus(); return }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(dateField.text.trim())) { error = "Date as YYYY-MM-DD."; dateField.forceActiveFocus(); return }
    if (!allDay && !/^\d{1,2}:\d{2}$/.test(startField.text.trim())) { error = "Start time as HH:MM (24 h)."; startField.forceActiveFocus(); return }
    if (!allDay && endField.text.trim() !== "" && !/^\d{1,2}:\d{2}$/.test(endField.text.trim())) { error = "End time as HH:MM."; endField.forceActiveFocus(); return }
    var spec = {
      calendarId: chosen ? chosen.id : "primary",
      summary: titleField.text.trim(),
      date: dateField.text.trim(),
      allDay: allDay,
      start: startField.text.trim().replace(/^(\d):/, "0$1:"),
      end: endField.text.trim().replace(/^(\d):/, "0$1:"),
      location: placeField.text.trim(),
      description: notesField.text.trim(),
      reminder: reminder
    }
    cal.act("add", ["add", JSON.stringify(spec)])
  }

  // `cal` arrives right after creation (Loader.onLoaded): fill the form then.
  property bool inited: false
  onCalChanged: if (cal && !inited) init()
  function init() {
    inited = true
    dateField.text = cal.selectedKey
    startField.text = defaultStart()
    endField.text = plusHour(startField.text)
    Qt.callLater(function() { titleField.forceActiveFocus() })
  }

  TextField {
    id: titleField
    width: parent.width
    foreground: Theme.foreground
    placeholderText: "Title (e.g. Dentist)"
    onAccepted: ne.save()
    Keys.onEscapePressed: ne.cal.back()
  }

  // ---- when
  Section { title: "WHEN" }
  RowLayout {
    width: parent.width
    spacing: Style.space(6)
    TextField {
      id: dateField
      Layout.preferredWidth: Style.space(130)
      foreground: Theme.foreground
      placeholderText: "YYYY-MM-DD"
      onAccepted: ne.save()
      Keys.onEscapePressed: ne.cal.back()
    }
    Button { text: "Today"; bordered: true; foreground: Theme.foreground; fontFamily: Theme.fontFamily; fontSize: Style.font.caption; onClicked: ne.shiftDate(0) }
    Button { text: "Tomorrow"; bordered: true; foreground: Theme.foreground; fontFamily: Theme.fontFamily; fontSize: Style.font.caption; onClicked: ne.shiftDate(1) }
    Item { Layout.fillWidth: true }
  }
  Text {
    text: /^\d{4}-\d{2}-\d{2}$/.test(dateField.text) && ne.cal ? ne.cal.dayTitle(dateField.text) : ""
    color: Theme.dim
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.caption
  }
  RowLayout {
    width: parent.width
    spacing: Style.space(8)
    Text { text: "All day"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
    ToggleSwitch { checked: ne.allDay; onToggled: ne.allDay = !ne.allDay }
    Item { Layout.fillWidth: true }
    Text { visible: !ne.allDay; text: "From"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.caption }
    TextField {
      id: startField
      visible: !ne.allDay
      Layout.preferredWidth: Style.space(72)
      foreground: Theme.foreground
      placeholderText: "09:00"
      onTextEdited: endField.text = ne.plusHour(text)
      onAccepted: ne.save()
      Keys.onEscapePressed: ne.cal.back()
    }
    Text { visible: !ne.allDay; text: "to"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.caption }
    TextField {
      id: endField
      visible: !ne.allDay
      Layout.preferredWidth: Style.space(72)
      foreground: Theme.foreground
      placeholderText: "10:00"
      onAccepted: ne.save()
      Keys.onEscapePressed: ne.cal.back()
    }
  }

  // ---- calendar + reminder
  Section { title: "CALENDAR" }
  Flow {
    width: parent.width
    spacing: Style.space(6)
    Repeater {
      model: ne.writable
      Button {
        required property var modelData
        text: "● " + modelData.summary
        selected: !!ne.chosen && ne.chosen.id === modelData.id
        bordered: true
        foreground: modelData.color
        fontFamily: Theme.fontFamily
        fontSize: Style.font.caption
        onClicked: ne.calendarId = modelData.id
      }
    }
  }
  Section { title: "REMINDER" }
  ButtonGroup {
    options: [
      { value: "default", label: "Default" }, { value: "-1", label: "None" }, { value: "10", label: "10 min" },
      { value: "30", label: "30 min" }, { value: "60", label: "1 h" }, { value: "1440", label: "1 day" }
    ]
    value: ne.reminder
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    fontSize: Style.font.caption
    focusable: false
    onChanged: function(v) { ne.reminder = v }
  }

  // ---- details
  Section { title: "DETAILS (OPTIONAL)" }
  TextField {
    id: placeField
    width: parent.width
    foreground: Theme.foreground
    placeholderText: "Place or address"
    onAccepted: ne.save()
    Keys.onEscapePressed: ne.cal.back()
  }
  TextField {
    id: notesField
    width: parent.width
    foreground: Theme.foreground
    placeholderText: "Notes"
    onAccepted: ne.save()
    Keys.onEscapePressed: ne.cal.back()
  }

  Notice { visible: ne.error !== ""; text: ne.error; level: "crit" }
  RowLayout {
    width: parent.width
    spacing: Style.space(6)
    Button {
      Layout.fillWidth: true
      text: ne.cal && ne.cal.busy === "add" ? "Saving…" : "Save to Google Calendar"
      iconText: "󰄬"
      bordered: true
      selected: true
      foreground: Theme.foreground
      fontFamily: Theme.fontFamily
      onClicked: ne.save()
    }
    Button {
      text: "Cancel"
      bordered: true
      foreground: Theme.foreground
      fontFamily: Theme.fontFamily
      onClicked: ne.cal.back()
    }
  }
  Caption { text: "Tab moves between fields · Enter saves · Esc cancels" }
}
