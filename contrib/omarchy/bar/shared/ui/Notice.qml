import QtQuick
import qs.Commons

// Boxed message. level: "crit" (urgent) | "warn" | "info".
Rectangle {
  id: notice
  property string text: ""
  property string level: "warn"
  readonly property color tint: level === "info" ? Theme.foreground : Theme.urgent
  width: parent ? parent.width : 0
  implicitHeight: noticeText.implicitHeight + Style.space(14)
  radius: Style.cornerRadius
  color: Qt.alpha(tint, level === "crit" ? 0.16 : 0.08)
  border.width: 1
  border.color: Qt.alpha(tint, level === "info" ? 0.25 : 0.45)
  Text {
    id: noticeText
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    anchors.leftMargin: Style.space(10)
    anchors.rightMargin: Style.space(10)
    text: notice.text
    textFormat: Text.PlainText
    color: notice.level === "crit" ? Theme.urgent : Theme.foreground
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.bodySmall
    font.bold: notice.level === "crit"
    wrapMode: Text.WordWrap
  }
}
