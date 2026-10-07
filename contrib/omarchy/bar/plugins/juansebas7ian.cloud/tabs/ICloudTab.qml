import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"

// iCloud Drive through the rclone mount at ~/iCloud: Apple's 30-day sign-in,
// documents with their sync state, activity, recent files, iCloud Photos.
Column {
  id: tab
  property var cloud: null
  spacing: Style.space(8)

  readonly property var ic: cloud ? cloud.ic : null
  readonly property var cache: cloud ? cloud.icCache : {}
  readonly property var listing: cloud ? cloud.icListing : null
  function stateGlyph(s) { return { folder: "󰉋", cloud: "☁", partial: "◐", local: "✓", queued: "⏳", uploading: "↑" }[s] || "·" }
  function stateText(s) { return { cloud: "in iCloud", partial: "partly on this PC", local: "on this PC", queued: "waiting to upload", uploading: "uploading" }[s] || "" }

  // Not set up yet
  Column {
    visible: !!tab.ic && !tab.ic.configured
    width: parent.width
    spacing: Style.space(6)
    Line {
      text: "Connect your Apple ID to sync iCloud Drive to ~/iCloud. First, on the iPhone turn on Settings › [your name] › iCloud › "
        + "“Access iCloud Data on the Web”. You will need your password and a 2FA code."
    }
    ActionRow { icon: "󰌾"; label: "Connect iCloud"; hint: "icloud-setup, in a terminal"; onActivated: tab.cloud.terminal("iCloud", "icloud-setup") }
  }

  RowLayout {
    visible: !!tab.ic && tab.ic.configured
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "Mounted at ~/iCloud"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text {
        text: !tab.ic ? "" : tab.cloud.busy === "icloud" ? "Working…" : tab.ic.mounted ? "rclone-icloud.service · " + tab.ic.service : "Off: documents are not available"
        color: Theme.dim
        font.family: Theme.fontFamily
        font.pixelSize: Style.font.caption
      }
    }
    ToggleSwitch {
      checked: !!tab.cloud && tab.cloud.icMounted
      busy: !!tab.cloud && tab.cloud.busy === "icloud"
      onToggled: tab.cloud.act("icloud", [tab.cloud.icMounted ? "unmount" : "mount"])
    }
  }
  Notice {
    visible: !!tab.cloud && tab.cloud.icConfigured && (tab.cloud.icFailing || tab.cloud.icAuthSoon)
    level: tab.cloud && tab.cloud.icAuthExpired ? "crit" : "warn"
    text: !tab.ic ? "" : tab.cloud.icAuthExpired
        ? "Apple's sign-in expired (it lasts 30 days). Renew it below and type the 2FA code." + (tab.ic.authError ? "\n" + tab.ic.authError : "")
      : tab.cloud.icAuthSoon ? "Apple's sign-in expires in " + Math.max(0, Math.floor(tab.cloud.icDaysLeft)) + " day(s): renew it below."
      : ((tab.cache.erroredFiles || 0) > 0 ? tab.cache.erroredFiles + " file(s) failed to upload. " : "")
        + (tab.ic.service === "failed" ? "The mount service failed: journalctl --user -u rclone-icloud" : (tab.ic.lastError || ""))
  }

  // ------------------------------------------------------------ account
  Section { visible: !!tab.ic && tab.ic.configured; title: "ACCOUNT" }
  Gauge {
    visible: !!tab.ic && !!tab.ic.configured
    label: "Apple sign-in"
    value: !tab.cloud || tab.cloud.icDaysLeft === null ? "date unknown (renew to start the count)"
      : tab.cloud.icDaysLeft <= 0 ? "expired" : "renew in " + Math.floor(tab.cloud.icDaysLeft) + " days"
    fraction: tab.cloud && tab.cloud.icDaysLeft !== null ? Math.max(0, tab.cloud.icDaysLeft) / 30 : 0
    hot: !!tab.cloud && tab.cloud.icAuthSoon
  }
  Pair { visible: !!tab.ic && !!tab.ic.quota && !!tab.ic.quota.total; label: "iCloud storage"; value: tab.ic && tab.ic.quota ? Theme.bytes(tab.ic.quota.used) + " of " + Theme.bytes(tab.ic.quota.total) : "" }
  Pair { visible: !!tab.cloud && tab.cloud.icMounted; label: "On this PC (cache)"; value: Theme.bytes(tab.cache.bytesUsed) + " · " + (tab.cache.files || 0) + " files" }
  RowLayout {
    visible: !!tab.ic && tab.ic.configured
    width: parent.width
    ColumnLayout {
      Layout.fillWidth: true
      spacing: 0
      Text { text: "iCloud Photos (read-only)"; color: Theme.foreground; font.family: Theme.fontFamily; font.pixelSize: Style.font.bodySmall }
      Text { text: tab.ic && tab.ic.photos.mounted ? "~/iCloudPhotos" : "off"; color: Theme.dim; font.family: Theme.fontFamily; font.pixelSize: Style.font.caption }
    }
    ToggleSwitch {
      checked: !!tab.ic && tab.ic.photos.mounted
      busy: !!tab.cloud && tab.cloud.busy === "icloud"
      onToggled: tab.cloud.act("icloud", [tab.ic.photos.mounted ? "photos-unmount" : "photos-mount"])
    }
  }
  ActionRow { visible: !!tab.ic && tab.ic.configured; icon: "󰌾"; label: "Renew Apple sign-in"; hint: "icloud-setup reconnect: only asks for the 2FA code"; onActivated: tab.cloud.terminal("iCloud", "icloud-setup reconnect") }

  Transfers { visible: !!tab.cloud && tab.cloud.icMounted; st: tab.ic }

  // ------------------------------------------------------------ documents
  Section {
    visible: !!tab.cloud && tab.cloud.icMounted
    title: "DOCUMENTS · " + (!tab.cloud || tab.cloud.icDir === "" ? "ICLOUD DRIVE" : tab.cloud.icDir.toUpperCase())
  }
  FileRow {
    visible: !!tab.cloud && tab.cloud.icMounted && tab.cloud.icDir !== ""
    glyph: "󰁍"
    name: "Back"
    detail: "Esc"
    onPicked: tab.cloud.icUp()
  }
  Caption {
    visible: !!tab.listing && tab.listing.ok && !!tab.cloud && tab.cloud.icMounted
    text: {
      if (!tab.listing || !tab.listing.counts) return ""
      var c = tab.listing.counts, parts = []
      if (c.folder) parts.push(c.folder + " folders")
      if (c.local) parts.push(c.local + " on this PC")
      if (c.partial) parts.push(c.partial + " partly")
      if (c.cloud) parts.push(c.cloud + " only in iCloud")
      if (c.queued || c.uploading) parts.push(((c.queued || 0) + (c.uploading || 0)) + " to upload")
      return parts.length ? parts.join(" · ") + " · ☁ cloud ◐ partly ✓ here · 󰇚 = download" : "Empty folder"
    }
  }
  Line {
    visible: !!tab.cloud && tab.cloud.icMounted && (!tab.listing || (!tab.listing.ok && tab.listing.error !== "not mounted"))
    text: !tab.listing ? "Loading…" : "Could not read the folder: " + tab.listing.error
    tone: !tab.listing ? "dim" : "urgent"
  }
  Repeater {
    model: tab.listing && tab.listing.ok && tab.cloud.icMounted ? tab.listing.entries : []
    FileRow {
      required property var modelData
      glyph: tab.stateGlyph(modelData.state)
      glyphHot: modelData.state === "queued" || modelData.state === "uploading"
      glyphOpacity: modelData.state === "cloud" ? 0.6 : 1.0
      name: modelData.name
      // iCloud gives folders no date: rclone reports 2000-01-01, so it is hidden.
      detail: modelData.dir ? "folder" + (modelData.mtime > 978307200 ? " · " + Theme.ago(modelData.mtime) : "")
        : Theme.bytes(modelData.size) + " · " + Theme.ago(modelData.mtime) + " · " + tab.stateText(modelData.state)
      actionGlyph: modelData.dir || modelData.state === "cloud" || modelData.state === "partial" ? "󰇚" : ""
      onPicked: modelData.dir ? tab.cloud.icList(modelData.path) : tab.cloud.openPath(tab.ic.mountpoint + "/" + modelData.path)
      onAction: tab.cloud.act("icloud", ["keep", modelData.path])
    }
  }

  Section { visible: !!tab.ic && (tab.ic.recent || []).length > 0; title: "RECENT ON THIS PC" }
  Repeater {
    model: tab.ic ? tab.ic.recent : []
    FileRow {
      required property var modelData
      glyph: "󰈔"
      name: modelData.name
      detail: (modelData.dir || "/") + " · " + Theme.bytes(modelData.size) + " · " + Theme.ago(modelData.mtime)
      onPicked: tab.cloud.openPath(tab.ic.mountpoint + "/" + modelData.path)
    }
  }

  Section { title: "OPEN" }
  ActionRow { visible: !!tab.cloud && tab.cloud.icMounted; icon: "󰉋"; label: "This folder"; hint: "In the file manager (o)"; onActivated: tab.cloud.openTabFolder() }
  ActionRow { icon: "󰖟"; label: "icloud.com"; hint: "In the browser"; onActivated: tab.cloud.openUrl("https://www.icloud.com/iclouddrive/") }
}
