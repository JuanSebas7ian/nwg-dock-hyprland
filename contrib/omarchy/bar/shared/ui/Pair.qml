import QtQuick
import qs.Commons

// One "label ........ value" row. hot = urgent color, strong = bold label,
// small = caption size.
Item {
  id: pair
  property string label: ""
  property string value: ""
  property bool hot: false
  property bool strong: false
  property bool small: false
  width: parent ? parent.width : 0
  implicitHeight: Math.max(pl.implicitHeight, pv.implicitHeight)
  Text {
    id: pl
    anchors.left: parent.left
    anchors.right: pv.left
    anchors.rightMargin: Style.space(8)
    text: pair.label
    textFormat: Text.PlainText
    color: pair.hot ? Theme.urgent : Theme.foreground
    opacity: pair.strong || pair.hot ? 1.0 : 0.6
    font.family: Theme.fontFamily
    font.pixelSize: pair.small ? Style.font.caption : Style.font.bodySmall
    font.bold: pair.strong
    elide: Text.ElideRight
  }
  Text {
    id: pv
    anchors.right: parent.right
    width: Math.min(implicitWidth, pair.width * 0.62)
    horizontalAlignment: Text.AlignRight
    text: pair.value
    textFormat: Text.PlainText
    color: pair.hot ? Theme.urgent : Theme.foreground
    font.family: Theme.fontFamily
    font.pixelSize: pair.small ? Style.font.caption : Style.font.bodySmall
    font.bold: pair.strong
    elide: Text.ElideLeft
  }
}
