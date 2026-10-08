import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// Row that opens another screen: leading glyph or flag, title, hint, chevron.
CursorSurface {
  id: nav
  property string icon: ""
  property string cc: ""
  property bool showFlag: false
  property string label: ""
  property string hint: ""
  property string trailing: ""
  property bool emphasis: false
  signal activated()
  width: parent ? parent.width : 0
  foreground: Theme.foreground
  bordered: emphasis
  hasCursor: navMouse.containsMouse
  implicitHeight: navRow.implicitHeight + Style.space(emphasis ? 18 : 12)
  MouseArea {
    id: navMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: nav.activated()
  }
  RowLayout {
    id: navRow
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.leftMargin: Style.space(10)
    anchors.rightMargin: Style.space(10)
    spacing: Style.space(10)
    Flag { visible: nav.showFlag; cc: nav.cc; size: nav.emphasis ? 26 : 20 }
    Text {
      visible: !nav.showFlag && nav.icon !== ""
      text: nav.icon
      color: Theme.foreground
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.icon
    }
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text {
        Layout.fillWidth: true
        text: nav.label
        textFormat: Text.PlainText
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: nav.emphasis ? Style.font.body : Style.font.bodySmall
        font.bold: nav.emphasis
        elide: Text.ElideRight
      }
      Text {
        Layout.fillWidth: true
        visible: nav.hint !== ""
        text: nav.hint
        textFormat: Text.PlainText
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
      }
    }
    Text {
      visible: nav.trailing !== ""
      text: nav.trailing
      color: Theme.dim
      font.family: Theme.fontFamily
      font.pixelSize: Style.font.caption
    }
    Text { text: "›"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.body }
  }
}
