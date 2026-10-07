"""Tests for bin/opencode-ollama-sync without Ollama: which models are listed,
limits and capabilities, and that user edits survive a sync.

Run: cd contrib/omarchy/bar && PYTHONDONTWRITEBYTECODE=1 python3 -m unittest tests.test_opencode_sync
"""

import unittest
from importlib.machinery import SourceFileLoader
from importlib.util import module_from_spec, spec_from_loader
from pathlib import Path

PATH = Path(__file__).resolve().parent.parent / "bin" / "opencode-ollama-sync"
_loader = SourceFileLoader("opencode_ollama_sync", str(PATH))
sync = module_from_spec(spec_from_loader(_loader.name, _loader))
_loader.exec_module(sync)


def show(caps, ctx):
  return {"capabilities": caps, "model_info": {"qwen2.context_length": ctx}}


class Sync(unittest.TestCase):
  def test_describe_skips_embeddings_and_caps_context(self):
    found = sync.describe([
      ("qwen2.5-coder:7b", show(["completion", "tools", "insert"], 131072)),
      ("nomic-embed-text:latest", show(["embedding"], 2048)),
      ("llava:7b", show(["completion", "vision"], 4096)),
      ("qwq:32b", show(["completion", "tools", "thinking"], 32768)),
    ], server_ctx=32768)
    self.assertEqual(sorted(found), ["llava:7b", "qwen2.5-coder:7b", "qwq:32b"])
    coder = found["qwen2.5-coder:7b"]
    self.assertEqual(coder["limit"], {"context": 32768, "output": 8192})
    self.assertTrue(coder["tool_call"])
    self.assertFalse(coder["reasoning"])
    self.assertEqual(found["llava:7b"]["limit"], {"context": 4096, "output": 1024})
    self.assertFalse(found["llava:7b"]["tool_call"])
    self.assertEqual(found["llava:7b"]["modalities"]["input"], ["text", "image"])
    self.assertTrue(found["qwq:32b"]["reasoning"])

  def test_merge_keeps_user_names_extra_keys_and_the_rest_of_the_file(self):
    config = {"$schema": "x", "autoupdate": False, "theme": "omarchy",
              "provider": {"anthropic": {"options": {}},
                           "ollama": {"npm": "@ai-sdk/openai-compatible", "options": {"baseURL": "http://h:1/v1"},
                                      "models": {"qwen2.5-coder:7b": {"name": "My coder", "options": {"temperature": 0.2}},
                                                 "gone:1b": {"name": "Deleted model"}}}}}
    found = sync.describe([("qwen2.5-coder:7b", show(["completion", "tools"], 32768)),
                           ("new:3b", show(["completion"], 8192))], 32768)
    new = sync.merge(config, found)
    models = new["provider"]["ollama"]["models"]
    self.assertEqual(sorted(models), ["new:3b", "qwen2.5-coder:7b"])         # gone:1b removed
    self.assertEqual(models["qwen2.5-coder:7b"]["name"], "My coder")         # rename kept
    self.assertEqual(models["qwen2.5-coder:7b"]["options"], {"temperature": 0.2})
    self.assertEqual(models["new:3b"]["name"], "new:3b (local)")
    self.assertEqual(new["provider"]["ollama"]["options"]["baseURL"], "http://h:1/v1")  # user URL kept
    self.assertEqual(new["provider"]["anthropic"], {"options": {}})
    self.assertEqual(new["theme"], "omarchy")
    self.assertEqual(config["provider"]["ollama"]["models"]["gone:1b"]["name"], "Deleted model")  # input untouched

  def test_merge_creates_the_provider(self):
    new = sync.merge({"$schema": "x"}, {"m:1b": {"name": "m:1b (local)", "tool_call": False, "reasoning": False,
                                                  "limit": {"context": 4096, "output": 1024}}})
    self.assertEqual(new["provider"]["ollama"]["npm"], "@ai-sdk/openai-compatible")
    self.assertIn("m:1b", new["provider"]["ollama"]["models"])

  def test_second_merge_is_a_no_op(self):
    found = sync.describe([("a:1b", show(["completion", "tools"], 4096))], 32768)
    once = sync.merge({}, found)
    self.assertEqual(sync.merge(once, found), once)


if __name__ == "__main__":
  unittest.main()
