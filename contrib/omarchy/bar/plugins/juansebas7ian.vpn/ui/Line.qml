import QtQuick
import qs.Commons

// Wrapped paragraph. tone: "normal" | "dim" | "urgent".
Text {
  property bool small: false
  property bool bold: false
  property string tone: "normal"
  width: parent ? parent.width : 0
  color: tone === "urgent" ? Theme.urgent : tone === "dim" ? Theme.dim : Theme.foreground
  font.family: Theme.fontFamily
  font.pixelSize: small ? Style.font.caption : Style.font.bodySmall
  font.bold: bold
  wrapMode: Text.WordWrap
  textFormat: Text.PlainText
}
