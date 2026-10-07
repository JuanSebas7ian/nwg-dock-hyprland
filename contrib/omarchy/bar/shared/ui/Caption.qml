import QtQuick
import qs.Commons

// Small dim label above a list ("HOME · click a folder").
Text {
  width: parent ? parent.width : 0
  color: Theme.dim
  font.family: Theme.fontFamily
  font.pixelSize: Style.font.caption
  elide: Text.ElideRight
  textFormat: Text.PlainText
}
