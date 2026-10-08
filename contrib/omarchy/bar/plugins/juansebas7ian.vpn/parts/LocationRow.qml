import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// One location in the picker: flag, country, city, load; marked when it is the active one.
CursorSurface {
  id: row
  property var loc: null
  property bool isCurrent: false
  property bool busy: false
  signal picked()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  hasCursor: rowMouse.containsMouse
  current: isCurrent
  implicitHeight: rowLayout.implicitHeight + Style.space(10)
  readonly property int load: loc && loc.load !== null && loc.load !== undefined ? loc.load : -1
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
    Flag { cc: row.loc ? row.loc.cc : ""; size: 22 }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        Layout.fillWidth: true
        text: row.loc ? row.loc.country : ""
        textFormat: Text.PlainText
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
        font.bold: row.isCurrent
        elide: Text.ElideRight
      }
      Text {
        Layout.fillWidth: true
        text: row.loc ? row.loc.city + (row.loc.virtual ? " · virtual" : "") : ""
        textFormat: Text.PlainText
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
      }
    }
    Text {
      visible: row.busy || row.isCurrent
      text: row.busy ? "Connecting…" : "● Connected"
      color: Theme.foreground
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.caption
      font.bold: true
    }
    // Server load: short bar + percent (fuller = busier).
    Row {
      visible: row.load >= 0 && !row.busy && !row.isCurrent
      spacing: Style.space(6)
      Rectangle {
        anchors.verticalCenter: parent.verticalCenter
        width: Style.space(34)
        height: Style.space(4)
        radius: height / 2
        color: Qt.alpha(Theme.foreground, 0.15)
        Rectangle {
          width: parent.width * Math.min(1, row.load / 100)
          height: parent.height
          radius: parent.radius
          color: row.load >= 80 ? Theme.urgent : Qt.alpha(Theme.foreground, 0.7)
        }
      }
      Text {
        width: Style.space(28)
        horizontalAlignment: Text.AlignRight
        text: row.load + "%"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
  }
}
