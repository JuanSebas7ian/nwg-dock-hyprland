import QtQuick
import qs.Commons

// Area chart of the last `capacity` samples, newest on the right, with an
// optional dashed warning line. Repaints only while visible.
Column {
  id: graph
  property string label: ""
  property var values: []
  property real minValue: 0
  property real maxValue: 100
  property real warnAt: -1
  property int capacity: 120
  width: parent ? parent.width : 0
  spacing: Style.space(3)

  Text {
    text: graph.label
    textFormat: Text.PlainText
    color: Theme.foreground
    opacity: 0.75
    font.family: Theme.fontFamily
    font.pixelSize: Style.font.caption
  }
  Canvas {
    id: canvas
    width: parent.width
    height: Style.space(54)
    onPaint: {
      var ctx = getContext("2d")
      ctx.reset()
      ctx.fillStyle = Qt.alpha(Theme.foreground, 0.07)
      ctx.fillRect(0, 0, width, height)
      var span = Math.max(1e-6, graph.maxValue - graph.minValue)
      function y(v) { return height - (Math.max(graph.minValue, Math.min(graph.maxValue, v)) - graph.minValue) / span * height }
      if (graph.warnAt > 0) {
        ctx.strokeStyle = Qt.alpha(Theme.urgent, 0.5)
        ctx.setLineDash([3, 3])
        ctx.beginPath(); ctx.moveTo(0, y(graph.warnAt)); ctx.lineTo(width, y(graph.warnAt)); ctx.stroke()
        ctx.setLineDash([])
      }
      var n = graph.values.length
      if (n < 2) return
      var step = width / (graph.capacity - 1)
      var x0 = width - (n - 1) * step
      ctx.beginPath()
      ctx.moveTo(x0, height)
      for (var i = 0; i < n; i++) ctx.lineTo(x0 + i * step, y(graph.values[i]))
      ctx.lineTo(width, height)
      ctx.closePath()
      ctx.fillStyle = Qt.alpha(Theme.foreground, 0.18)
      ctx.fill()
      ctx.beginPath()
      for (var j = 0; j < n; j++) {
        if (j === 0) ctx.moveTo(x0, y(graph.values[0]))
        else ctx.lineTo(x0 + j * step, y(graph.values[j]))
      }
      ctx.strokeStyle = graph.warnAt > 0 && graph.values[n - 1] >= graph.warnAt ? Theme.urgent : Theme.foreground
      ctx.lineWidth = 1.5
      ctx.stroke()
    }
  }
  onValuesChanged: if (visible) canvas.requestPaint()
  onVisibleChanged: if (visible) canvas.requestPaint()
}
