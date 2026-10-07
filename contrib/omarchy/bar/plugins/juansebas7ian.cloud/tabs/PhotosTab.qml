import QtQuick
import QtQuick.Controls
import QtQuick.Layouts
import Quickshell
import qs.Commons
import qs.Ui
import "../ui"

// Google Photos uploads (gphotos-sync): progress, sources with their switch,
// the last photos uploaded. Upload-only: Google's API cannot read the rest.
Column {
  id: tab
  property var cloud: null
  spacing: Style.space(8)

  readonly property var gp: cloud ? cloud.gp : null
  function duration(s) {
    s = Number(s || 0)
    if (s <= 0) return ""
    if (s < 3600) return Math.max(1, Math.round(s / 60)) + " min"
    return Math.floor(s / 3600) + " h " + Math.round((s % 3600) / 60) + " min"
  }

  RowLayout {
    visible: !!tab.cloud && !tab.cloud.gpNeedsSetup
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Upload automatically"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        text: !tab.gp ? "" : tab.cloud.gpPaused ? "Paused" + (tab.cloud.gpPending > 0 ? " · " + tab.cloud.gpPending + " pending" : "")
          : tab.cloud.gpRunning ? "Uploading · " + (tab.gp.filesDone || 0) + " of " + (tab.gp.filesTotal || 0)
          : tab.gp.state === "quota" ? "Google's daily quota is used up: it continues later"
          : tab.cloud.gpPending > 0 ? tab.cloud.gpPending + " pending · next run within 30 min"
          : "Up to date" + (tab.gp.lastOk ? " · " + Theme.ago(tab.gp.lastOk) : "")
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
    ToggleSwitch {
      checked: !!tab.cloud && !tab.cloud.gpPaused
      busy: !!tab.cloud && tab.cloud.busy === "gphotos"
      onToggled: tab.cloud.act("gphotos", [tab.cloud.gpPaused ? "resume" : "pause"])
    }
  }

  // Not connected
  Column {
    visible: !!tab.cloud && tab.cloud.gpNeedsSetup
    width: parent.width
    spacing: Style.space(6)
    Line {
      text: tab.cloud && tab.cloud.gpState === "auth" ? "Google withdrew the permission. Reconnect to keep uploading."
        : tab.cloud && tab.cloud.gpState === "missing" ? "gphotos-sync is not installed: run contrib/omarchy/bar/install.sh."
        : "Not connected yet. gphotos-setup walks you through creating your own Google client ID (5 min) and the permission. "
          + "Then ~/GoogleFotos, screenshots, Dropbox camera uploads and the photos in Google Drive upload by themselves, each folder to its album."
    }
    ActionRow {
      visible: !!tab.cloud && tab.cloud.gpState !== "missing"
      icon: "󰌆"
      label: tab.cloud && tab.cloud.gpState === "auth" ? "Reconnect Google Photos" : "Connect Google Photos"
      hint: "gphotos-setup, in a terminal"
      onActivated: tab.cloud.terminal("GoogleFotos", tab.cloud.gpState === "auth" ? "gphotos-setup reconnect" : "gphotos-setup")
    }
  }
  Notice {
    visible: !!tab.gp && (tab.gp.error || "") !== "" && !tab.cloud.gpNeedsSetup
    level: tab.gp && tab.gp.state === "quota" ? "info" : "crit"
    text: tab.gp ? (tab.gp.error || "") : ""
  }

  // Uploading now
  Column {
    visible: !!tab.cloud && tab.cloud.gpRunning
    width: parent.width
    spacing: Style.space(4)
    Section { title: "UPLOADING" }
    Gauge {
      label: tab.gp && tab.gp.album ? "Album: " + tab.gp.album : "Preparing…"
      value: tab.gp ? Theme.bytes(tab.gp.bytesDone) + " of " + Theme.bytes(tab.gp.bytesTotal) : ""
      fraction: tab.gp && tab.gp.bytesTotal > 0 ? tab.gp.bytesDone / tab.gp.bytesTotal : 0
    }
    Pair {
      label: tab.gp ? (tab.gp.filesDone || 0) + " of " + (tab.gp.filesTotal || 0) + " files" : ""
      value: tab.gp && tab.gp.speed > 0 ? Theme.rate(tab.gp.speed) + (tab.gp.eta ? " · " + tab.duration(tab.gp.eta) + " left" : "") : ""
    }
    Repeater {
      model: tab.gp ? (tab.gp.transferring || []) : []
      Pair { required property var modelData; label: "↑ " + modelData.name; value: modelData.percent + "%" }
    }
  }

  // Sources
  Section { visible: !!tab.gp && !tab.cloud.gpNeedsSetup; title: "SOURCES" }
  Repeater {
    model: tab.gp && !tab.cloud.gpNeedsSetup ? (tab.gp.sources || []) : []
    RowLayout {
      required property var modelData
      width: tab.width
      ColumnLayout {
        Layout.fillWidth: true
        spacing: 0
        Text {
          Layout.fillWidth: true
          text: modelData.name
          color: Theme.foreground
          opacity: modelData.enabled ? 1 : 0.5
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.bodySmall
          elide: Text.ElideRight
        }
        Text {
          Layout.fillWidth: true
          text: modelData.error ? modelData.error
            : modelData.total === 0 && !tab.gp.lastRun ? "Not scanned yet"
            : modelData.uploaded + " of " + modelData.total + " uploaded"
              + (modelData.pending > 0 ? " · " + modelData.pending + " to go (" + Theme.bytes(modelData.pendingBytes) + ")" : " ✓")
          color: modelData.error ? Theme.urgent : Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.caption
          elide: Text.ElideRight
        }
      }
      ToggleSwitch {
        checked: modelData.enabled
        busy: tab.cloud.busy === "gphotos"
        onToggled: tab.cloud.act("gphotos", ["source", modelData.id, modelData.enabled ? "off" : "on"])
      }
    }
  }

  // Recent uploads
  Section { visible: !!tab.gp && (tab.gp.recent || []).length > 0; title: "RECENT UPLOADS" }
  Grid {
    id: grid
    visible: !!tab.gp && (tab.gp.recent || []).length > 0
    columns: 4
    spacing: Style.space(6)
    readonly property real cell: (tab.width - spacing * (columns - 1)) / columns
    Repeater {
      model: tab.gp ? (tab.gp.recent || []) : []
      CursorSurface {
        id: thumb
        required property var modelData
        width: grid.cell
        height: grid.cell
        foreground: Theme.foreground
        hasCursor: thumbMouse.containsMouse
        Rectangle { anchors.fill: parent; radius: Style.space(4); color: Qt.alpha(Theme.foreground, 0.08) }
        Image {
          anchors.fill: parent
          anchors.margins: Style.space(2)
          visible: (thumb.modelData.thumb || "") !== ""
          source: thumb.modelData.thumb ? "file://" + thumb.modelData.thumb : ""
          sourceSize.width: 256
          sourceSize.height: 256
          fillMode: Image.PreserveAspectCrop
          asynchronous: true
          cache: false
        }
        Text {
          anchors.centerIn: parent
          visible: (thumb.modelData.thumb || "") === ""
          text: thumb.modelData.kind === "video" ? "󰕧" : tab.cloud.glyphPhotos
          color: Theme.dim
          font.family: Theme.fontFamily
          font.pixelSize: Style.font.display
        }
        MouseArea {
          id: thumbMouse
          anchors.fill: parent
          hoverEnabled: true
          cursorShape: thumb.modelData.path ? Qt.PointingHandCursor : Qt.ArrowCursor
          onClicked: if (thumb.modelData.path) tab.cloud.openPath(thumb.modelData.path)
          ToolTip.visible: containsMouse
          ToolTip.delay: 400
          ToolTip.text: thumb.modelData.rel + "\n→ " + thumb.modelData.album + " · " + Theme.ago(thumb.modelData.t)
        }
      }
    }
  }

  Section { title: "OPEN" }
  ActionRow { visible: !!tab.cloud && !tab.cloud.gpNeedsSetup; icon: "󰑐"; label: "Sync now"; hint: "r"; onActivated: tab.cloud.photosSyncNow() }
  ActionRow { icon: "󰉋"; label: "~/GoogleFotos"; hint: "Anything copied there is uploaded (o)"; onActivated: tab.cloud.openPath(Quickshell.env("HOME") + "/GoogleFotos") }
  ActionRow { icon: "󰖟"; label: "photos.google.com"; hint: "In the browser"; onActivated: tab.cloud.openUrl("https://photos.google.com") }
}
