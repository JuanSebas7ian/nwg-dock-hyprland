import QtQuick
import qs.Commons
import qs.Ui

// File or folder row: leading glyph, name, dim detail line, optional trailing
// action glyph. Click the row = picked(); click the trailing glyph = action().
CursorSurface {
  id: row
  property string glyph: ""
  property string name: ""
  property string detail: ""
  property string actionGlyph: ""
  property bool glyphHot: false
  property real glyphOpacity: 1.0
  signal picked()
  signal action()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  hasCursor: rowMouse.containsMouse || actMouse.containsMouse
  implicitHeight: rowCol.implicitHeight + Style.space(8)
  MouseArea {
    id: rowMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: row.picked()
  }
  Text {
    id: lead
    anchors.left: parent.left
    anchors.leftMargin: Style.space(8)
    anchors.verticalCenter: parent.verticalCenter
    width: row.glyph !== "" ? Style.space(18) : 0
    text: row.glyph
    color: row.glyphHot ? Theme.urgent : Theme.foreground
    opacity: row.glyphOpacity
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.bodySmall
  }
  Column {
    id: rowCol
    anchors.left: lead.right
    anchors.right: trail.left
    anchors.leftMargin: row.glyph !== "" ? 0 : Style.space(8)
    anchors.rightMargin: Style.space(6)
    anchors.verticalCenter: parent.verticalCenter
    Text {
      width: parent.width
      text: row.name
      textFormat: Text.PlainText
      color: Theme.foreground
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.bodySmall
      elide: Text.ElideMiddle
    }
    Text {
      width: parent.width
      visible: row.detail !== ""
      text: row.detail
      textFormat: Text.PlainText
      color: Theme.dim
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.caption
      elide: Text.ElideMiddle
    }
  }
  Text {
    id: trail
    anchors.right: parent.right
    anchors.rightMargin: Style.space(8)
    anchors.verticalCenter: parent.verticalCenter
    visible: row.actionGlyph !== ""
    width: visible ? implicitWidth : 0
    text: row.actionGlyph
    color: Theme.foreground
    opacity: actMouse.containsMouse ? 1.0 : 0.6
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.body
    MouseArea {
      id: actMouse
      anchors.fill: parent
      anchors.margins: -Style.space(4)
      hoverEnabled: true
      cursorShape: Qt.PointingHandCursor
      onClicked: row.action()
    }
  }
}
