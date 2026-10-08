import QtQuick
import qs.Commons

// Thin horizontal fill bar (0..1).
Item {
  id: meter
  property real fraction: 0
  property bool hot: false
  property real thickness: Style.space(5)
  width: parent ? parent.width : 0
  height: thickness
  Rectangle { anchors.fill: parent; radius: height / 2; color: Qt.alpha(Theme.foreground, 0.14) }
  Rectangle {
    height: parent.height
    radius: height / 2
    color: meter.hot ? Theme.urgent : Theme.foreground
    width: parent.width * Math.max(0, Math.min(1, meter.fraction))
    Behavior on width { NumberAnimation { duration: 400; easing.type: Easing.OutCubic } }
  }
}
