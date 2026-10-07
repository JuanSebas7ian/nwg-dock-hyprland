import QtQuick
import QtQuick.Layouts
import qs.Commons
import Quickshell
import qs.Ui
import "../ui"

// Storage: disks with partitions and NVMe health, what uses the system disk
// (click a folder to look inside; Esc goes back), Steam per game, disk
// firmware and endurance, what is available and what can be freed.
Column {
  id: tab
  property var hw: null
  spacing: Style.space(8)

  readonly property var ov: hw ? hw.storage.overview : null
  readonly property var bd: hw ? hw.storage.breakdown : null
  readonly property var cl: hw ? hw.storage.cleanup : null
  readonly property var steam: hw ? hw.storage.steam : null
  readonly property var fw: hw ? hw.storage.firmware : null
  readonly property var dir: hw ? hw.dirView : null
  readonly property bool lowSpace: !!hw && hw.alerts.some(function(a) { return a.id === "disk-free" })

  Repeater {
    model: tab.hw ? tab.hw.alertsFor("storage") : []
    Notice { required property var modelData; level: modelData.level; text: modelData.title + " · " + modelData.detail }
  }

  // ================================================= folder drill-down
  Column {
    visible: !!tab.dir
    width: parent.width
    spacing: Style.space(4)
    RowLayout {
      width: parent.width
      PanelActionButton { iconText: "󰁍"; tooltipText: "Back (Esc)"; foreground: Theme.foreground; fontFamily: Theme.fontFamily; onClicked: tab.hw.back() }
      Text {
        Layout.fillWidth: true
        text: tab.dir ? tab.dir.path + "  ·  " + Theme.bytes(tab.dir.total) : ""
        textFormat: Text.PlainText
        color: Theme.foreground
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.bodySmall
        elide: Text.ElideLeft
      }
      PanelActionButton {
        iconText: "󰉋"
        tooltipText: "Open in the file manager"
        foreground: Theme.foreground
        fontFamily: Theme.fontFamily
        onClicked: tab.hw.runAndClose("xdg-open " + tab.hw.bar.shellQuote(tab.dir.path))
      }
    }
    Repeater {
      model: tab.dir ? tab.dir.items : []
      SizeRow {
        required property var modelData
        label: (modelData.isDir ? "󰉋 " : "󰈔 ") + modelData.name
        size: modelData.size
        total: tab.dir.total
        clickable: modelData.isDir
        onPicked: tab.hw.openDir(modelData.path)
      }
    }
  }

  Column {
    visible: !tab.dir
    width: parent.width
    spacing: Style.space(8)

    // ================================================= disks
    Line { visible: !tab.ov; text: "Reading disks…"; tone: "dim" }
    Repeater {
      model: tab.ov ? tab.ov.disks : []
      Column {
        id: disk
        required property var modelData
        width: tab.width
        spacing: Style.space(3)
        readonly property var h: modelData.health
        readonly property var sys: modelData.partitions.filter(function(p) { return p.mounts.indexOf("/") >= 0 })[0]
        Section { title: "󰋊 " + disk.modelData.model.toUpperCase() + (disk.modelData.role ? " · " + disk.modelData.role.toUpperCase() : "") + " · " + Theme.bytes(disk.modelData.size) }
        Meter { visible: !!disk.sys; fraction: disk.sys ? disk.sys.used / disk.sys.fssize : 0; hot: tab.lowSpace }
        Pair { visible: !!disk.sys; label: "Used · free"; value: disk.sys ? Theme.bytes(disk.sys.used) + " · " + Theme.bytes(disk.sys.avail) : "" }
        Pair {
          visible: !!disk.h
          label: "Health"
          value: disk.h ? (disk.h.percentUsed !== null ? disk.h.percentUsed + "% worn" : "") + " · spare " + disk.h.spare + "%"
            + (disk.h.mediaErrors > 0 ? " · " + disk.h.mediaErrors + " media errors" : "") : ""
          hot: !!disk.h && (disk.h.mediaErrors > 0 || disk.h.percentUsed >= 80 || (disk.h.criticalWarning || []).length > 0)
        }
        Pair {
          visible: !!disk.h
          label: "Written · read · on"
          value: disk.h ? Theme.bytes(disk.h.written) + " · " + Theme.bytes(disk.h.read) + " · " + Math.round(disk.h.powerOnHours / 24) + " days · " + disk.h.temp + "°C" : ""
        }
        Repeater {
          model: disk.modelData.partitions
          Pair {
            required property var modelData
            small: true
            label: "    ".repeat(modelData.depth) + "└ " + modelData.name + "  " + (modelData.fstype || modelData.type)
              + (modelData.label ? " \"" + modelData.label + "\"" : "") + (modelData.mounts.length > 0 ? "  " + modelData.mounts.join(" ") : "")
            value: modelData.used !== null && modelData.fssize ? Theme.bytes(modelData.used) + " / " + Theme.bytes(modelData.fssize)
              : Theme.bytes(modelData.size) + (modelData.fstype === "ntfs" ? " (not mounted)" : "")
          }
        }
      }
    }

    // ================================================= what uses it
    Section { title: "WHAT USES THE SYSTEM DISK" }
    Line { visible: !tab.bd; text: "Measuring folders… (du, about 5 s)"; tone: "dim" }
    Item {
      visible: !!tab.bd && !!tab.ov
      width: parent.width
      height: Style.space(10)
      readonly property real total: tab.ov ? tab.ov.root.size : 1
      Rectangle { anchors.fill: parent; radius: Style.space(3); color: Qt.alpha(Theme.foreground, 0.12) }
      Row {
        anchors.fill: parent
        Rectangle { height: parent.height; color: Theme.foreground; width: tab.bd && tab.ov ? parent.width * Math.min(tab.bd.home.total, tab.ov.root.used) / parent.parent.total : 0 }
        Rectangle { height: parent.height; color: Qt.alpha(Theme.foreground, 0.55); width: tab.bd && tab.ov ? parent.width * Math.max(0, tab.ov.root.used - tab.bd.home.total) / parent.parent.total : 0 }
      }
    }
    Pair { visible: !!tab.bd; label: "■ Your files (home)"; value: tab.bd ? Theme.bytes(tab.bd.home.total) : "" }
    Pair { visible: !!tab.bd; label: "■ System, apps, swap, snapshots"; value: tab.bd ? Theme.bytes(tab.bd.system.total + tab.bd.other) : "" }
    Pair { visible: !!tab.bd && tab.bd.overMeasured > 0; small: true; label: "zstd compression saves about"; value: tab.bd ? Theme.bytes(tab.bd.overMeasured) : "" }
    Caption { visible: !!tab.bd; text: "HOME · click a folder to see inside" }
    Repeater {
      model: tab.bd ? tab.bd.home.items : []
      SizeRow { required property var modelData; label: "󰉋 " + modelData.name; size: modelData.size; total: tab.bd.home.total; onPicked: tab.hw.openDir(modelData.path) }
    }
    Caption { visible: !!tab.bd; text: "SYSTEM" }
    Repeater {
      model: tab.bd ? tab.bd.system.items : []
      SizeRow { required property var modelData; label: modelData.name; size: modelData.size; total: tab.bd.system.total; onPicked: tab.hw.openDir(modelData.path) }
    }
    SizeRow {
      visible: !!tab.bd && tab.bd.other > 0
      label: "Snapshots, metadata, unreadable folders"
      size: tab.bd ? tab.bd.other : 0
      total: tab.bd ? tab.bd.system.total : 1
      clickable: false
    }

    // ================================================= steam
    Section { visible: !!tab.steam && tab.steam.games.length + tab.steam.tools.length > 0; title: "STEAM · " + (tab.steam ? Theme.bytes(tab.steam.total) : "") }
    Repeater {
      model: tab.steam ? tab.steam.games : []
      Column {
        id: game
        required property var modelData
        width: tab.width
        SizeRow { label: "󰊗 " + game.modelData.name; size: game.modelData.total; total: tab.steam.total; onPicked: tab.hw.openDir(game.modelData.path) }
        Caption {
          leftPadding: Style.space(8)
          text: "game " + Theme.bytes(game.modelData.size)
            + (game.modelData.prefix > 0 ? " · Proton prefix " + Theme.bytes(game.modelData.prefix) : "")
            + (game.modelData.shaders > 0 ? " · shaders " + Theme.bytes(game.modelData.shaders) : "")
            + (game.modelData.lastPlayed > 0 ? " · played " + Theme.ago(game.modelData.lastPlayed) : " · never played")
        }
      }
    }
    Repeater {
      model: tab.steam ? tab.steam.tools : []
      SizeRow { required property var modelData; label: "󰏗 " + modelData.name; size: modelData.total; total: tab.steam.total; onPicked: tab.hw.openDir(modelData.path) }
    }
    SizeRow {
      visible: !!tab.steam && tab.steam.client > 0
      label: "Steam client, downloads, logs"
      size: tab.steam ? tab.steam.client : 0
      total: tab.steam ? tab.steam.total : 1
      onPicked: tab.hw.openDir(tab.steam.path)
    }

    // ================================================= firmware & endurance
    Section { visible: !!tab.fw; title: "DISK FIRMWARE & ENDURANCE" }
    Repeater {
      model: tab.fw ? tab.fw.drives : []
      Column {
        id: drv
        required property var modelData
        width: tab.width
        spacing: Style.space(3)
        readonly property real tbwUsed: drv.modelData.tbw && drv.modelData.written ? drv.modelData.written / (drv.modelData.tbw * 1e12) : -1
        Pair { label: drv.modelData.model; value: ""; strong: true }
        Pair {
          label: "Firmware (vs " + drv.modelData.source + ")"
          value: drv.modelData.firmware + (drv.modelData.upToDate ? " · up to date ✓" : " → " + drv.modelData.latest + " available")
          hot: !drv.modelData.upToDate
        }
        Pair { visible: drv.tbwUsed >= 0; label: "Endurance (TBW)"; value: Theme.bytes(drv.modelData.written) + " of " + drv.modelData.tbw + " TB · " + Math.round(drv.tbwUsed * 100) + "%"; hot: drv.tbwUsed >= 0.8 }
        Meter { visible: drv.tbwUsed >= 0; fraction: drv.tbwUsed; hot: drv.tbwUsed >= 0.8 }
        Pair {
          visible: !!drv.modelData.health && drv.modelData.health.unsafeShutdowns !== undefined
          small: true
          label: "Unsafe shutdowns · media errors"
          value: drv.modelData.health ? drv.modelData.health.unsafeShutdowns + " · " + drv.modelData.health.mediaErrors : ""
        }
        Pair {
          visible: !!drv.modelData.selftest && drv.modelData.selftest.status !== ""
          small: true
          label: "Last self-test"
          value: drv.modelData.selftest ? drv.modelData.selftest.status + (drv.modelData.selftest.remaining > 0 ? " · " + drv.modelData.selftest.remaining + "% left" : "") : ""
        }
        Row {
          spacing: Style.space(6)
          PanelActionButton {
            visible: drv.modelData.udisks !== ""
            iconText: "󰙨"
            tooltipText: "Run a short SMART self-test (asks for your password)"
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            onClicked: tab.hw.send({ cmd: "selftest", obj: drv.modelData.udisks })
          }
          PanelActionButton {
            visible: drv.modelData.download !== ""
            iconText: "󰇚"
            tooltipText: drv.modelData.vendor === "Samsung" ? "Samsung firmware ISO (boot it from USB to update)" : "Update with fwupd"
            foreground: Theme.foreground
            fontFamily: Theme.fontFamily
            onClicked: drv.modelData.download.indexOf("http") === 0
              ? tab.hw.runAndClose("xdg-open " + tab.hw.bar.shellQuote(drv.modelData.download))
              : tab.hw.inTerminal(drv.modelData.download)
          }
        }
      }
    }

    // ================================================= available
    Section { title: "AVAILABLE" }
    Pair { visible: !!tab.ov; label: "System disk (btrfs)"; value: tab.ov ? Theme.bytes(tab.ov.root.free) + " free" : "" }
    Pair {
      visible: !!tab.ov && !!tab.ov.btrfs.freeMin
      small: true
      label: "Never allocated · worst case"
      value: tab.ov && tab.ov.btrfs.unallocated ? Theme.bytes(tab.ov.btrfs.unallocated) + " · " + Theme.bytes(tab.ov.btrfs.freeMin) : ""
    }
    Pair {
      visible: !!tab.ov
      label: "/boot (EFI)"
      value: {
        if (!tab.ov) return ""
        for (var i = 0; i < tab.ov.disks.length; i++) {
          var b = tab.ov.disks[i].partitions.filter(function(p) { return p.mounts.indexOf("/boot") >= 0 })[0]
          if (b) return Theme.bytes(b.avail) + " free of " + Theme.bytes(b.fssize)
        }
        return ""
      }
    }
    Repeater {
      model: tab.ov ? tab.ov.extra : []
      Pair { required property var modelData; label: modelData.name; value: Theme.bytes(modelData.free) + " free of " + Theme.bytes(modelData.size) }
    }
    Repeater {
      model: tab.ov ? tab.ov.swaps : []
      Pair { required property var modelData; small: true; label: "Swap " + modelData.name; value: Theme.bytes(modelData.used) + " / " + Theme.bytes(modelData.size) }
    }

    // ================================================= reclaim
    Section { visible: !!tab.cl; title: "CAN BE FREED" }
    Repeater {
      model: tab.cl ? tab.cl.items : []
      ActionRow {
        required property var modelData
        icon: modelData.command ? "󰃢" : "󰉋"
        label: modelData.name + " · " + Theme.bytes(modelData.size)
        hint: (modelData.detail ? modelData.detail + " · " : "") + (modelData.command ? "click: " + modelData.command : "click to review")
        onActivated: modelData.command ? tab.hw.inTerminal(modelData.command) : tab.hw.openDir(modelData.open)
      }
    }
    ActionRow {
      icon: ""
      label: "Explore interactively"
      hint: "dua interactive ~"
      onActivated: tab.hw.inTerminal("dua interactive " + Quickshell.env("HOME"))
    }
  }
}
