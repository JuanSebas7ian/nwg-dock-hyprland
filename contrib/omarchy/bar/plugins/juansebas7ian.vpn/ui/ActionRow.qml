import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui

// Full-width clickable row: icon, label, dim hint underneath.
CursorSurface {
  id: action
  property string icon: ""
  property string label: ""
  property string hint: ""
  signal activated()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  hasCursor: actionMouse.containsMouse
  implicitHeight: actionRow.implicitHeight + Style.spacing.rowPaddingX
  MouseArea {
    id: actionMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: action.activated()
  }
  RowLayout {
    id: actionRow
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.leftMargin: Style.space(10)
    anchors.rightMargin: Style.space(10)
    spacing: Style.space(8)
    Text { text: action.icon; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.icon }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: action.label; textFormat: Text.PlainText; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.body }
      Text {
        Layout.fillWidth: true
        visible: action.hint !== ""
        text: action.hint
        textFormat: Text.PlainText
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
      }
    }
  }
}
