import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../parts"
import "../Model.js" as Model

// Agenda: every event from today on, day by day.
Column {
  id: ag
  property var cal: null
  spacing: Style.space(6)
  property int span: 30

  readonly property var days: {
    if (!cal) return []
    var out = [], t = cal.today
    for (var i = 0; i < span; i++) {
      var k = Model.keyForDate(new Date(t.getFullYear(), t.getMonth(), t.getDate() + i))
      var evs = cal.byDay[k]
      if (evs && evs.length) out.push({ key: k, evs: evs })
    }
    return out
  }

  Line {
    visible: ag.days.length === 0
    tone: "dim"
    text: ag.cal && ag.cal.gcConfigured ? "Nothing in the next " + ag.span + " days." : "Connect Google Calendar first (back to the month)."
  }
  Repeater {
    model: ag.days
    Column {
      id: dayBlock
      required property var modelData
      width: ag.width
      spacing: Style.space(2)
      Section { title: ag.cal.dayTitle(dayBlock.modelData.key).toUpperCase() }
      Repeater {
        model: dayBlock.modelData.evs
        EventRow {
          required property var modelData
          ev: modelData
          cal: ag.cal
          onPicked: ag.cal.showEvent(modelData)
        }
      }
    }
  }
  RowLayout {
    width: parent.width
    spacing: Style.space(6)
    Button {
      Layout.fillWidth: true
      visible: ag.span < 365
      text: "Show " + (ag.span < 90 ? "3 months" : "a year")
      bordered: true
      foreground: Theme.foreground
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      onClicked: ag.span = ag.span < 90 ? 90 : 365
    }
    Button {
      Layout.fillWidth: true
      text: "New event"
      iconText: "󰐕"
      bordered: true
      selected: true
      foreground: Theme.foreground
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      onClicked: ag.cal.go("new")
    }
  }
}
