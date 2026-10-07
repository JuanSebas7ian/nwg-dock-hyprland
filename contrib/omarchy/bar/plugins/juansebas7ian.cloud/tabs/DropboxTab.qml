import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// Dropbox: the daemon (pause/resume), space here, recent files.
Column {
  id: tab
  property var cloud: null
  spacing: Style.space(8)

  readonly property var db: cloud ? cloud.db : null

  Line { visible: !!tab.db && !tab.db.installed; text: "Dropbox is not installed (omarchy install dropbox)."; tone: "dim" }
  RowLayout {
    visible: !!tab.db && tab.db.installed
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Sync"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        Layout.fillWidth: true
        text: tab.db ? (tab.cloud.busy === "dropbox" ? "Working…" : tab.db.statusText || "") : ""
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
        elide: Text.ElideRight
      }
    }
    ToggleSwitch {
      checked: !!tab.db && tab.db.running
      busy: !!tab.cloud && tab.cloud.busy === "dropbox"
      onToggled: tab.cloud.act("dropbox", [tab.db.running ? "stop" : "start"])
    }
  }
  Line {
    visible: !!tab.db && tab.db.installed && !tab.db.authenticated
    text: "Not signed in. Starting Dropbox prints a link to sign in."
  }
  ActionRow {
    visible: !!tab.db && tab.db.installed && !tab.db.authenticated
    icon: "󰌆"
    label: "Sign in to Dropbox"
    hint: "dropbox-cli start, in a terminal"
    onActivated: tab.cloud.terminal("Dropbox", "dropbox-cli start")
  }

  Section { visible: !!tab.db && tab.db.authenticated; title: "SPACE" }
  Pair { visible: !!tab.db && tab.db.authenticated; label: "Files on this PC"; value: tab.db ? Theme.bytes(tab.db.usedBytes) : "" }
  Pair {
    visible: !!tab.db && tab.db.authenticated && !!tab.db.plan
    label: "Plan"
    value: tab.db ? tab.db.plan + (tab.db.quotaKnown ? " · " + Theme.bytes(tab.db.quotaBytes) : " · quota not known here") : ""
  }

  Section { visible: !!tab.db && (tab.db.files || []).length > 0; title: "RECENT" }
  Repeater {
    model: tab.db ? (tab.db.files || []).filter(function(f) { return f.name.charAt(0) !== "." }) : []
    FileRow {
      required property var modelData
      glyph: "󰈔"
      name: modelData.name
      detail: modelData.folder + " · " + Theme.bytes(modelData.sizeBytes) + " · " + Theme.ago(modelData.modifiedTs)
      onPicked: tab.cloud.openPath(modelData.path)
    }
  }

  Section { title: "OPEN" }
  ActionRow { visible: !!tab.db && !!tab.db.accountPath; icon: "󰉋"; label: "~/Dropbox"; hint: "In the file manager (o)"; onActivated: tab.cloud.openPath(tab.db.accountPath) }
  ActionRow { icon: "󰖟"; label: "dropbox.com"; hint: "In the browser"; onActivated: tab.cloud.openUrl("https://www.dropbox.com/home") }
}
