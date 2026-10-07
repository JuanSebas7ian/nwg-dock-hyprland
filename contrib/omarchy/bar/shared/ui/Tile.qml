import QtQuick
import qs.Commons
import qs.Ui

// Summary card: icon + big value + caption, optional meter. Click = picked().
CursorSurface {
  id: tile
  property string icon: ""
  property string value: ""
  property string caption: ""
  property real fraction: -1
  property bool hot: false
  signal picked()
  foreground: Theme.foreground
  bordered: true
  hasCursor: tileMouse.containsMouse
  implicitHeight: tileCol.implicitHeight + Style.space(16)
  MouseArea {
    id: tileMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: tile.picked()
  }
  Column {
    id: tileCol
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.margins: Style.space(10)
    spacing: Style.space(3)
    Row {
      spacing: Style.space(6)
      Text {
        text: tile.icon
        color: tile.hot ? Theme.urgent : Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.icon
        anchors.verticalCenter: parent.verticalCenter
      }
      Text {
        text: tile.value
        textFormat: Text.PlainText
        color: tile.hot ? Theme.urgent : Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.body
        font.bold: true
        anchors.verticalCenter: parent.verticalCenter
      }
    }
    Text {
      width: parent.width
      text: tile.caption
      textFormat: Text.PlainText
      color: Theme.dim
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.caption
      elide: Text.ElideRight
    }
    Meter { visible: tile.fraction >= 0; fraction: tile.fraction; hot: tile.hot; thickness: Style.space(3) }
  }
}
