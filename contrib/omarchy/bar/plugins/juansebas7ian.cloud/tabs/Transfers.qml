import QtQuick
import qs.Commons
import "../ui"

// Transfers in flight and the upload queue of an rclone mount (gdrive / icloud status).
Column {
  id: tr
  property var st: null
  readonly property bool idle: !st || ((st.transfers || []).length === 0 && (st.queue || []).length === 0)
  width: parent ? parent.width : 0
  spacing: Style.space(6)

  Section { title: "ACTIVITY" }
  Line { visible: tr.idle; text: "Nothing uploading or downloading."; tone: "dim" }
  Repeater {
    model: tr.st ? tr.st.transfers : []
    Column {
      required property var modelData
      width: tr.width
      spacing: Style.space(2)
      Pair { label: modelData.name; value: modelData.percent + "% · " + Theme.rate(modelData.speed) }
      Meter { fraction: modelData.percent / 100; thickness: Style.space(4) }
    }
  }
  Repeater {
    model: tr.st ? tr.st.queue : []
    Pair {
      required property var modelData
      label: (modelData.uploading ? "↑ " : "⏳ ") + modelData.name
      value: Theme.bytes(modelData.size) + (modelData.tries > 0 ? " · retry " + modelData.tries : "")
    }
  }
}
