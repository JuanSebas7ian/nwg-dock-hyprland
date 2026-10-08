import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// One event: calendar color bar, time, title, place. Click = open it.
CursorSurface {
  id: row
  property var ev: null
  property var cal: null
  signal picked()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  hasCursor: rowMouse.containsMouse
  implicitHeight: rowLayout.implicitHeight + Style.space(10)
  readonly property bool past: !!ev && !!cal && cal.dateOf(ev.end) < new Date()
  MouseArea {
    id: rowMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: row.picked()
  }
  RowLayout {
    id: rowLayout
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.leftMargin: Style.space(8)
    anchors.rightMargin: Style.space(10)
    spacing: Style.space(10)
    Rectangle {
      Layout.preferredWidth: Style.space(4)
      Layout.fillHeight: true
      radius: width / 2
      color: row.ev ? row.ev.color : "transparent"
      opacity: row.past ? 0.45 : 1
    }
    Text {
      Layout.preferredWidth: Style.space(92)
      text: row.ev && row.cal ? row.cal.timeLabel(row.ev) : ""
      color: Theme.dim
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.caption
    }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        Layout.fillWidth: true
        text: row.ev ? row.ev.summary : ""
        textFormat: Text.PlainText
        color: Theme.foreground
        opacity: row.past ? 0.55 : 1
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: !row.past
        elide: Text.ElideRight
      }
      Text {
        Layout.fillWidth: true
        visible: !!row.ev && (row.ev.location !== "" || row.ev.meet !== "")
        text: row.ev ? (row.ev.meet !== "" ? "󰍫 Meet" + (row.ev.location ? " · " : "") : "") + row.ev.location : ""
        textFormat: Text.PlainText
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
      }
    }
    Text { text: "›"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.body }
  }
}
