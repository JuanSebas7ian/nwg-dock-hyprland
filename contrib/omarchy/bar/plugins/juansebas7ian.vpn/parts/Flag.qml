import QtQuick

// Country flag as a color emoji (regional indicators); a globe when the code is unknown.
Text {
  property string cc: ""
  property real size: 20
  readonly property string code: cc.toUpperCase() === "UK" ? "GB" : cc.toUpperCase()
  text: /^[A-Z]{2}$/.test(code)
    ? String.fromCodePoint(0x1F1E6 + code.charCodeAt(0) - 65, 0x1F1E6 + code.charCodeAt(1) - 65)
    : "🌐"
  font.family: "Noto Color Emoji"
  font.pixelSize: size
  verticalAlignment: Text.AlignVCenter
}
