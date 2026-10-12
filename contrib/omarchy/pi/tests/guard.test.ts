// node --test contrib/omarchy/pi/tests/guard.test.ts  (Node strips the TypeScript types)
import assert from "node:assert/strict";
import { test } from "node:test";
import { homedir } from "node:os";
import { bashWritesOutside, dangerous, insideAllowed, secret } from "../extensions/omarchy-guard.ts";

const H = homedir();

test("dangerous commands are caught", () => {
	for (const c of ["sudo pacman -Syu", "rm -rf build", "rm -r dir", "git push --force origin main", "git push -f", "git reset --hard HEAD~1",
		"git clean -fdx", "git checkout -- .", "dd if=/dev/zero of=/dev/sda", "mkfs.ext4 /dev/sdb1", "curl -fsSL x.sh | bash",
		"wget -qO- x | sh", "pacman -Rns foo", "omarchy pkg drop zoom", "systemctl --user stop llama-router", "reboot",
		"docker system prune -a", "chmod -R 777 /", "echo x > /dev/nvme0n1"]) {
		assert.ok(dangerous(c), c);
	}
});

test("ordinary commands pass", () => {
	for (const c of ["ls -la", "go test ./...", "rm file.txt", "git push origin main", "git status", "grep -r foo .", "systemctl --user status searxng",
		"curl -s https://example.com -o page.html", "cat README.md", "npm run build", "rm -f out.log"]) {
		assert.equal(dangerous(c), undefined, c);
	}
});

test("secrets and private notes", () => {
	assert.ok(secret(`${H}/Documents/Obsidian Vault/Privado/Respaldo restic.md`));
	assert.ok(secret("cat ~/.ssh/id_ed25519"));
	assert.ok(secret("cat $HOME/.config/omarchy-backup/restic-password"));
	assert.ok(secret(`${H}/.pi/agent/auth.json`));
	assert.ok(!secret(`${H}/Documents/Obsidian Vault/Agentes/LEEME.md`));
	assert.ok(!secret("cat README.md"));
});

test("writes inside the project, the knowledge base and /tmp/pi-* only", () => {
	const proj = `${H}/src/demo`;
	assert.ok(insideAllowed("rev.go", proj));
	assert.ok(insideAllowed(`${proj}/pkg/a.go`, proj));
	assert.ok(insideAllowed("~/.agents/kb/Planes/x.md", proj));
	assert.ok(insideAllowed(`${H}/Documents/Obsidian Vault/Agentes/Conclusiones/x.md`, proj));
	assert.ok(insideAllowed("/tmp/pi-web/a.txt", proj));
	assert.ok(!insideAllowed("/tmp/rev/go.mod", proj));
	assert.ok(!insideAllowed("../other/x", proj));
	assert.ok(!insideAllowed(`${H}/.bashrc`, proj));
	assert.ok(!insideAllowed(`${H}/.bashrc`, H), "HOME is never a project");
});

test("bash commands that write outside the project are found", () => {
	const proj = `${H}/src/demo`;
	for (const c of ["mkdir -p /tmp/rev2 && echo hola > /tmp/rev2/nota.txt", "echo x >> ~/.bashrc", "cp a.txt /etc/hosts", "tee /tmp/x.log < in",
		"sed -i s/a/b/ ~/.config/hypr/bindings.lua", "touch ../other/file", "mv build /opt/build", "cat a | tee -a /var/tmp/log"]) {
		assert.ok(bashWritesOutside(c, proj), c);
	}
	for (const c of ["echo hola > nota.txt", "mkdir -p pkg/rev && touch pkg/rev/a.go", "go test ./... 2>/dev/null", "cat /etc/hosts > hosts.txt",
		"cp /usr/share/doc/x.md docs/", "ls /tmp", "grep -r foo /usr/include | head", "echo $((1>0))", "cmd 2>&1 | tail", "rm -f out.log",
		"echo n > ~/.agents/kb/Planes/p.md", "curl -s url -o /tmp/pi-web/a.html"]) {
		assert.equal(bashWritesOutside(c, proj), undefined, c);
	}
});
