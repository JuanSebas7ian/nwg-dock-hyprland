import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// Google Drive through the rclone mount at ~/GoogleDrive.
Column {
  id: tab
  property var cloud: null
  spacing: Style.space(8)

  readonly property var gd: cloud ? cloud.gd : null
  readonly property var q: gd ? gd.quota : null
  readonly property var cache: cloud ? cloud.gdCache : {}

  RowLayout {
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Mounted at ~/GoogleDrive"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        text: !tab.gd ? "" : tab.cloud.busy === "gdrive" ? "Working…" : tab.gd.mounted ? "rclone-gdrive.service · " + tab.gd.service : "Off: files are not available"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
    ToggleSwitch {
      checked: !!tab.cloud && tab.cloud.gdMounted
      busy: !!tab.cloud && tab.cloud.busy === "gdrive"
      onToggled: tab.cloud.act("gdrive", [tab.cloud.gdMounted ? "unmount" : "mount"])
    }
  }
  Notice {
    visible: !!tab.cloud && tab.cloud.gdFailing || (!!tab.gd && (tab.gd.lastError || "") !== "")
    level: "crit"
    text: !tab.gd ? "" : ((tab.cache.erroredFiles || 0) > 0 ? tab.cache.erroredFiles + " file(s) failed to upload. " : "")
      + (tab.gd.service === "failed" ? "The mount service failed: journalctl --user -u rclone-gdrive" : (tab.gd.lastError || tab.gd.error || ""))
  }

  Section { title: "STORAGE" }
  Gauge {
    visible: !!tab.q
    label: "Used"
    value: tab.q ? Theme.bytes(tab.q.used + (tab.q.other || 0)) + " of " + Theme.bytes(tab.q.total) : ""
    fraction: tab.q && tab.q.total ? (tab.q.used + (tab.q.other || 0)) / tab.q.total : 0
  }
  Pair { visible: !!tab.q; label: "Drive · Gmail/Photos · trash"; value: tab.q ? Theme.bytes(tab.q.used) + " · " + Theme.bytes(tab.q.other) + " · " + Theme.bytes(tab.q.trashed) : "" }
  Pair { visible: !!tab.cloud && tab.cloud.gdMounted; label: "On this PC (cache)"; value: Theme.bytes(tab.cache.bytesUsed) + " · " + (tab.cache.files || 0) + " files" }

  Transfers { visible: !!tab.cloud && tab.cloud.gdMounted; st: tab.gd }

  Section { visible: !!tab.gd && (tab.gd.recent || []).length > 0; title: "RECENT ON THIS PC" }
  Repeater {
    model: tab.gd ? tab.gd.recent : []
    FileRow {
      required property var modelData
      glyph: "󰈔"
      name: modelData.name
      detail: (modelData.dir || "/") + " · " + Theme.bytes(modelData.size) + " · " + Theme.ago(modelData.mtime)
      onPicked: tab.cloud.openPath(tab.gd.mountpoint + "/" + modelData.path)
    }
  }

  Section { title: "OPEN" }
  ActionRow { visible: !!tab.cloud && tab.cloud.gdMounted; icon: "󰉋"; label: "~/GoogleDrive"; hint: "In the file manager (o)"; onActivated: tab.cloud.openPath(tab.gd.mountpoint) }
  ActionRow { icon: "󰖟"; label: "drive.google.com"; hint: "In the browser"; onActivated: tab.cloud.openUrl("https://drive.google.com") }
}
