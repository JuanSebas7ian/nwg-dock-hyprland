import QtQuick
import qs.Commons
import qs.Ui

// Clickable row with a background bar proportional to size/total.
CursorSurface {
  id: sr
  property string label: ""
  property real size: 0
  property real total: 1
  property bool clickable: true
  signal picked()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  hasCursor: srMouse.containsMouse && clickable
  implicitHeight: Style.space(26)
  Rectangle {
    anchors.left: parent.left
    anchors.top: parent.top
    anchors.bottom: parent.bottom
    radius: Style.space(3)
    color: Qt.alpha(Theme.foreground, 0.1)
    width: parent.width * Math.max(0, Math.min(1, sr.size / Math.max(1, sr.total)))
  }
  MouseArea {
    id: srMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: sr.clickable ? Qt.PointingHandCursor : Qt.ArrowCursor
    onClicked: if (sr.clickable) sr.picked()
  }
  Text {
    anchors.left: parent.left
    anchors.leftMargin: Style.space(6)
    anchors.right: srSize.left
    anchors.rightMargin: Style.space(8)
    anchors.verticalCenter: parent.verticalCenter
    text: sr.label
    textFormat: Text.PlainText
    color: Theme.foreground
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.bodySmall
    elide: Text.ElideMiddle
  }
  Text {
    id: srSize
    anchors.right: parent.right
    anchors.rightMargin: Style.space(6)
    anchors.verticalCenter: parent.verticalCenter
    text: Theme.bytes(sr.size)
    color: Theme.foreground
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.bodySmall
  }
}
