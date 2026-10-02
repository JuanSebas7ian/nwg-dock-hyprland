-- Fragmentos para ~/.config/hypr de Omarchy (configuración en Lua).
-- Cada bloque va al final del archivo indicado.

-- ~/.config/hypr/autostart.lua: arranca el dock (con su vigilante) al iniciar sesión.
o.launch_on_start(os.getenv("HOME") .. "/.config/hypr/scripts/launch-dock.sh")

-- ~/.config/hypr/looknfeel.lua: desenfoca lo que hay detrás del dock (efecto cristal).
-- Solo se nota si el desenfoque global está activo (decoration.blur.enabled, que Omarchy trae apagado).
hl.layer_rule({ match = { namespace = "nwg-dock" }, blur = true, ignore_alpha = 0.05 })

-- ~/.config/hypr/bindings.lua: emergencia, mata el dock aunque esté colgado; launch-dock.sh lo relanza.
o.bind("SUPER + CTRL + SHIFT + D", "Restart dock", "pkill -9 -x nwg-dock-hyprla")
