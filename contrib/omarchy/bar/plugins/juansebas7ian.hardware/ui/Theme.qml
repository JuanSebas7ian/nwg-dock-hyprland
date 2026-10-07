pragma Singleton
import QtQuick
import qs.Commons

// Colors, font and formatting shared by the juansebas7ian.* panels.
// The panel that imports this folder binds foreground/urgent/fontFamily to
// its bar so every component follows the theme without passing them around.
QtObject {
  property color foreground: Color.foreground
  property color urgent: Color.urgent
  property string fontFamily: Style.font.family
  readonly property color dim: Qt.darker(foreground, 1.55)

  function bytes(n) {
    n = Number(n || 0)
    if (n >= 1e12) return (n / 1e12).toFixed(2) + " TB"
    if (n >= 1e9) return (n / 1e9).toFixed(1) + " GB"
    if (n >= 1e6) return (n / 1e6).toFixed(0) + " MB"
    if (n >= 1e3) return (n / 1e3).toFixed(0) + " KB"
    return n + " B"
  }
  function rate(bps) { return bytes(bps) + "/s" }
  function gb(n) { return (Number(n || 0) / 1073741824).toFixed(1) }
  function deg(t) { return t === null || t === undefined ? "–" : Math.round(t) + "°C" }
  function pct(f) { return Math.round(Number(f || 0) * 100) + "%" }
  function ago(epoch) {
    if (!epoch) return ""
    var s = Math.max(0, Date.now() / 1000 - epoch)
    if (s < 90) return "just now"
    if (s < 5400) return Math.round(s / 60) + " min ago"
    if (s < 129600) return Math.round(s / 3600) + " h ago"
    return Math.round(s / 86400) + " days ago"
  }
}
