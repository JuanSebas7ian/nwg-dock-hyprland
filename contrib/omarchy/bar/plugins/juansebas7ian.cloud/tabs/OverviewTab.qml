import QtQuick
import qs.Commons
import "../ui"

// Overview: what needs attention, then one tile per service (click = its tab).
Column {
  id: tab
  property var cloud: null
  spacing: Style.space(10)

  Line { visible: !tab.cloud || !tab.cloud.st; text: "Checking the services…"; tone: "dim" }
  Repeater {
    model: tab.cloud ? tab.cloud.issues : []
    MouseArea {
      required property var modelData
      width: tab.width
      height: note.implicitHeight
      cursorShape: Qt.PointingHandCursor
      onClicked: tab.cloud.setTab(modelData.tab)
      Notice { id: note; width: parent.width; level: modelData.level; text: modelData.text }
    }
  }

  Grid {
    id: grid
    visible: !!tab.cloud && !!tab.cloud.st
    width: parent.width
    columns: 2
    columnSpacing: Style.space(8)
    rowSpacing: Style.space(8)
    readonly property real cell: (width - columnSpacing) / 2

    Tile {
      readonly property var q: tab.cloud && tab.cloud.gd ? tab.cloud.gd.quota : null
      width: grid.cell
      icon: tab.cloud ? tab.cloud.glyphDrive : ""
      value: !tab.cloud || !tab.cloud.gdMounted ? "Not mounted" : tab.cloud.gdPending > 0 ? "↑ " + tab.cloud.gdPending : "Up to date"
      caption: "Google Drive" + (q ? " · " + Theme.bytes(q.free) + " free" : "")
      fraction: q && q.total ? (q.used + (q.other || 0)) / q.total : -1
      hot: !!tab.cloud && tab.cloud.gdFailing
      onPicked: tab.cloud.setTab("drive")
    }
    Tile {
      width: grid.cell
      icon: tab.cloud ? tab.cloud.glyphICloud : ""
      value: !tab.cloud ? "" : !tab.cloud.icConfigured ? "Not connected" : tab.cloud.icAuthExpired ? "Sign in again"
        : !tab.cloud.icMounted ? "Not mounted" : tab.cloud.icPending > 0 ? "↑ " + tab.cloud.icPending : "Up to date"
      caption: "iCloud Drive" + (tab.cloud && tab.cloud.icDaysLeft !== null ? " · sign-in " + Math.max(0, Math.floor(tab.cloud.icDaysLeft)) + " d left" : "")
      fraction: tab.cloud && tab.cloud.icDaysLeft !== null ? Math.max(0, tab.cloud.icDaysLeft) / 30 : -1
      hot: !!tab.cloud && (tab.cloud.icFailing || tab.cloud.icAuthSoon)
      onPicked: tab.cloud.setTab("icloud")
    }
    Tile {
      width: grid.cell
      icon: tab.cloud ? tab.cloud.glyphPhotos : ""
      value: !tab.cloud ? "" : tab.cloud.gpNeedsSetup ? "Not connected" : tab.cloud.gpPaused ? "Paused"
        : tab.cloud.gpRunning ? "Uploading" : tab.cloud.gpPending > 0 ? tab.cloud.gpPending + " pending" : "Up to date"
      caption: "Google Photos" + (tab.cloud && tab.cloud.gp && tab.cloud.gp.uploadedTotal ? " · " + tab.cloud.gp.uploadedTotal + " uploaded" : "")
      hot: !!tab.cloud && tab.cloud.gpFailing
      onPicked: tab.cloud.setTab("photos")
    }
    Tile {
      width: grid.cell
      icon: tab.cloud ? tab.cloud.glyphDropbox : ""
      value: !tab.cloud || !tab.cloud.db ? "" : !tab.cloud.db.installed ? "Not installed" : tab.cloud.db.statusText || "Stopped"
      caption: "Dropbox" + (tab.cloud && tab.cloud.db && tab.cloud.db.usedBytes ? " · " + Theme.bytes(tab.cloud.db.usedBytes) + " here" : "")
      onPicked: tab.cloud.setTab("dropbox")
    }
  }

  Caption { text: "Keys: h/l or 1–5 switch tabs · j/k scroll · r re-read · o open the folder · Esc close" }
}
