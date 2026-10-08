import QtQuick
import qs.Commons

// Pair + Meter: "RAM ..... 12.1 / 31.2 GB" with a bar underneath.
Column {
  id: gauge
  property string label: ""
  property string value: ""
  property real fraction: 0
  property bool hot: false
  width: parent ? parent.width : 0
  spacing: Style.space(3)
  Pair { label: gauge.label; value: gauge.value; hot: gauge.hot }
  Meter { fraction: gauge.fraction; hot: gauge.hot }
}
