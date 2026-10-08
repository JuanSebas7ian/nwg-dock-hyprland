import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// One event: everything about it, open it in Google, join Meet, delete.
Column {
  id: es
  property var cal: null
  spacing: Style.space(8)

  readonly property var ev: cal ? cal.openEvent : null
  property bool confirm: false

  RowLayout {
    width: parent.width
    spacing: Style.space(12)
    Rectangle { Layout.preferredWidth: Style.space(6); Layout.fillHeight: true; radius: width / 2; color: es.ev ? es.ev.color : "transparent" }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: Style.space(2)
      Text {
        Layout.fillWidth: true
        text: es.ev ? es.ev.summary : ""
        textFormat: Text.PlainText
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.body * 1.25
        font.bold: true
        wrapMode: Text.WordWrap
      }
      Text {
        text: es.ev && es.cal ? es.cal.dayTitle(es.cal.daysOf(es.ev)[0] || "") + " · " + es.cal.timeLabel(es.ev) : ""
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
      }
      Text {
        text: es.ev ? es.ev.calendar + (es.ev.recurring ? " · repeats" : "") : ""
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }

  Pair { visible: !!es.ev && es.ev.location !== ""; label: "Where"; value: es.ev ? es.ev.location : "" }
  Pair {
    visible: !!es.ev
    label: "Reminders"
    value: !es.ev || !es.ev.reminders.length ? "none" : es.ev.reminders.map(function(m) {
      return m === 0 ? "at start" : m < 60 ? m + " min" : m < 1440 ? (m / 60) + " h" : (m / 1440) + " d"
    }).join(", ") + " before"
  }
  Line {
    visible: !!es.ev && es.ev.description !== ""
    small: true
    text: es.ev ? es.ev.description.replace(/<[^>]+>/g, " ").replace(/&nbsp;/g, " ").trim() : ""
  }

  Section { title: "ACTIONS" }
  ActionRow {
    visible: !!es.ev && es.ev.meet !== ""
    icon: "󰍫"
    label: "Join Google Meet"
    hint: es.ev ? es.ev.meet.replace("https://", "") : ""
    onActivated: es.cal.openUrl(es.ev.meet)
  }
  ActionRow {
    visible: !!es.ev && es.ev.location !== ""
    icon: "󰍎"
    label: "Open the place in Maps"
    hint: es.ev ? es.ev.location : ""
    onActivated: es.cal.openUrl("https://www.google.com/maps/search/?api=1&query=" + encodeURIComponent(es.ev.location))
  }
  ActionRow {
    visible: !!es.ev && es.ev.link !== ""
    icon: "󰖟"
    label: "Open in Google Calendar"
    hint: "Edit, invite people, change the color…"
    onActivated: es.cal.openUrl(es.ev.link)
  }
  ActionRow {
    visible: !!es.ev && es.ev.canEdit && !es.confirm
    icon: "󰆴"
    label: "Delete event"
    hint: es.ev && es.ev.recurring ? "Asks: only this one or the whole series" : "Asks once more"
    onActivated: es.confirm = true
  }
  Line { visible: !!es.ev && !es.ev.canEdit; small: true; tone: "dim"; text: "Read-only calendar: it can't be deleted from here." }

  Notice { visible: es.confirm; level: "crit"; text: "Delete “" + (es.ev ? es.ev.summary : "") + "” from Google Calendar?" }
  RowLayout {
    visible: es.confirm
    width: parent.width
    spacing: Style.space(6)
    Button {
      Layout.fillWidth: true
      text: es.cal && es.cal.busy === "delete" ? "Deleting…" : es.ev && es.ev.recurring ? "Only this one" : "Delete"
      bordered: true
      foreground: Theme.urgent
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      onClicked: es.cal.act("delete", ["delete", es.ev.calendarId, es.ev.id])
    }
    Button {
      Layout.fillWidth: true
      visible: !!es.ev && es.ev.recurring
      text: "Whole series"
      bordered: true
      foreground: Theme.urgent
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      onClicked: es.cal.act("delete", ["delete", es.ev.calendarId, es.ev.id, "--series"])
    }
    Button {
      Layout.fillWidth: true
      text: "Cancel"
      bordered: true
      foreground: Theme.foreground
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      onClicked: es.confirm = false
    }
  }
}
