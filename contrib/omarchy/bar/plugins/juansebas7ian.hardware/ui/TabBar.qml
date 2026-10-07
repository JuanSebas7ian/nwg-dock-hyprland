import QtQuick
import qs.Commons
import qs.Ui

// Row of equal-width tab buttons. tabs: [{ id, label, badge?, hot? }].
// A badge (number or "!") is appended to the label; hot tabs use the urgent color.
Row {
  id: bar
  property var tabs: []
  property string current: ""
  property bool cursorActive: false
  signal picked(string id)
  width: parent ? parent.width : 0
  spacing: Style.space(4)
  readonly property real cellWidth: tabs.length > 0 ? (width - spacing * (tabs.length - 1)) / tabs.length : 0

  Repeater {
    model: bar.tabs
    Button {
      required property var modelData
      width: bar.cellWidth
      text: modelData.label + (modelData.badge ? " " + modelData.badge : "")
      selected: modelData.id === bar.current
      hasCursor: bar.cursorActive && modelData.id === bar.current
      bordered: true
      foreground: modelData.hot ? Theme.urgent : Theme.foreground
      fontFamily: Theme.fontFamily
      fontSize: Style.font.caption
      horizontalPadding: Style.space(4)
      verticalPadding: Style.spacing.controlPaddingY
      onClicked: bar.picked(modelData.id)
    }
  }
}
