import QtQuick
import QtQuick.Layouts
import qs.Commons
import qs.Ui
import "../ui"
import "../parts"

// Location picker: provider, search, region filter, recent ones, then every
// location grouped by region with its flag and server load. A click connects.
Column {
  id: pick
  property var vpn: null
  spacing: Style.space(8)

  property string query: ""
  property string region: "all"

  readonly property string provider: vpn ? vpn.provider : "surfshark"
  readonly property var ld: vpn ? vpn.locs : null
  readonly property var all: ld ? (ld[provider] || []) : []
  readonly property var regionOptions: [
    { value: "all", label: "All" },
    { value: "The Americas", label: "Americas" },
    { value: "Europe", label: "Europe" },
    { value: "Asia Pacific", label: "Asia" },
    { value: "Middle East and Africa", label: "Africa · ME" }
  ]

  function matches(l) {
    if (region !== "all" && l.region !== region) return false
    if (query === "") return true
    var q = query.toLowerCase()
    return (l.country + " " + l.city + " " + l.cc).toLowerCase().indexOf(q) >= 0
  }
  readonly property var filtered: all.filter(matches)
  readonly property var groups: {
    var order = ld ? ld.regions : []
    var by = {}
    filtered.forEach(function(l) { (by[l.region || "Other"] = by[l.region || "Other"] || []).push(l) })
    var out = []
    order.concat(Object.keys(by).filter(function(r) { return order.indexOf(r) < 0 })).forEach(function(r) {
      if (by[r]) out.push({ region: r, items: by[r] })
    })
    return out
  }
  readonly property var recent: {
    if (!ld || query !== "" || region !== "all") return []
    var out = []
    ;(ld.recent || []).forEach(function(r) {
      if (r.provider !== pick.provider) return
      var hit = pick.all.filter(function(l) { return l.id === r.id })[0]
      if (hit) out.push(hit)
    })
    return out
  }
  function isCurrent(l) { return !!vpn && vpn.connected && !!vpn.currentLoc && vpn.currentLoc.provider === l.provider && vpn.currentLoc.id === l.id }
  function typeText(t) {
    if (t === "\b") { query = query.slice(0, -1); return }
    search.text = search.text + t
    search.forceActiveFocus()
  }

  // ---- provider
  TabBar {
    tabs: [
      { id: "surfshark", label: "Surfshark", badge: pick.ld ? String(pick.ld.surfshark.length) : "" },
      { id: "proton", label: "Proton Free", badge: pick.ld ? String(pick.ld.proton.length) : "" }
    ]
    current: pick.provider
    onPicked: function(id) { pick.vpn.provider = id }
  }

  // ---- search + region
  TextField {
    id: search
    width: parent.width
    foreground: Theme.foreground
    placeholderText: "Search a country or city…"
    onTextChanged: pick.query = text.trim()
    Keys.onEscapePressed: { if (text !== "") text = ""; else pick.vpn.back() }
    onAccepted: if (pick.filtered.length > 0) pick.vpn.connectTo(pick.filtered[0])
  }
  ButtonGroup {
    visible: pick.provider === "surfshark"
    options: pick.regionOptions
    value: pick.region
    foreground: Theme.foreground
    fontFamily: Theme.fontFamily
    fontSize: Style.font.caption
    focusable: false
    onChanged: function(v) { pick.region = v }
  }

  // ---- not ready / empty
  Notice {
    visible: pick.provider === "surfshark" && !!pick.vpn && !pick.vpn.surfsharkReady
    level: "info"
    text: "Surfshark needs your WireGuard key once. Pick a location to set it up, or open Accounts & servers."
  }
  Column {
    visible: pick.provider === "proton" && pick.all.length === 0
    width: parent.width
    spacing: Style.space(6)
    Line {
      tone: "dim"
      text: "No Proton servers yet. The free plan has servers in about 10 countries; download one WireGuard .conf per server at account.protonvpn.com → Downloads, then import them."
    }
    NavRow { icon: "󰀉"; label: "Accounts & servers"; hint: "Import from ~/Downloads"; onActivated: pick.vpn.go("accounts") }
  }
  Line { visible: !pick.ld; tone: "dim"; text: "Loading locations…" }
  Line { visible: !!pick.ld && pick.all.length > 0 && pick.filtered.length === 0; tone: "dim"; text: "Nothing matches “" + pick.query + "”." }

  // ---- recent
  Section { visible: pick.recent.length > 0; title: "🕘  RECENT" }
  Repeater {
    model: pick.recent
    LocationRow {
      required property var modelData
      loc: modelData
      isCurrent: pick.isCurrent(modelData)
      busy: !!pick.vpn && pick.vpn.busy === modelData.id
      onPicked: pick.vpn.connectTo(modelData)
    }
  }

  // ---- by region
  Repeater {
    model: pick.groups
    Column {
      id: grp
      required property var modelData
      width: pick.width
      spacing: Style.space(2)
      Section { title: pick.vpn.regionGlyph(grp.modelData.region) + "  " + grp.modelData.region.toUpperCase() + " · " + grp.modelData.items.length }
      Repeater {
        model: grp.modelData.items
        LocationRow {
          required property var modelData
          loc: modelData
          isCurrent: pick.isCurrent(modelData)
          busy: !!pick.vpn && pick.vpn.busy === modelData.id
          onPicked: pick.vpn.connectTo(modelData)
        }
      }
    }
  }
  Caption {
    visible: pick.provider === "surfshark" && pick.all.length > 0
    text: "Bar = server load (lower is faster). Virtual = IP of that country, server elsewhere."
  }
}
