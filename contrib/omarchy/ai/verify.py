"""Checks the AI environment really uses the GPU. Exit 0 = all good.

  ~/ai/.venv/bin/python verify.py            (ai-lab)
  ~/ai/unsloth-env/.venv/bin/python verify.py    (also imports unsloth)
"""

import importlib
import sys
import time

ok = True


def check(name, fn):
    global ok
    try:
        msg = fn()
        print(f"OK   {name}: {msg}")
    except Exception as exc:  # report every failure, keep going
        ok = False
        print(f"FAIL {name}: {type(exc).__name__}: {str(exc).splitlines()[0][:160]}")


import torch  # noqa: E402

check("torch", lambda: f"{torch.__version__}, CUDA runtime {torch.version.cuda}, cuDNN {torch.backends.cudnn.version()}")


def gpu():
    assert torch.cuda.is_available(), "torch.cuda.is_available() is False"
    p = torch.cuda.get_device_properties(0)
    return f"{p.name}, {p.total_memory / 2**30:.1f} GiB, compute {p.major}.{p.minor}"


check("GPU", gpu)


def matmul():
    a = torch.randn(4096, 4096, device="cuda", dtype=torch.bfloat16)
    b = torch.randn(4096, 4096, device="cuda", dtype=torch.bfloat16)
    torch.cuda.synchronize()
    t = time.perf_counter()
    for _ in range(20):
        a @ b
    torch.cuda.synchronize()
    dt = (time.perf_counter() - t) / 20
    return f"bf16 4096² matmul {2 * 4096**3 / dt / 1e12:.1f} TFLOPS"


check("bf16 matmul", matmul)


def sdpa():
    from torch.nn.attention import SDPBackend, sdpa_kernel
    q = torch.randn(2, 8, 1024, 64, device="cuda", dtype=torch.bfloat16)
    with sdpa_kernel([SDPBackend.FLASH_ATTENTION, SDPBackend.EFFICIENT_ATTENTION]):
        torch.nn.functional.scaled_dot_product_attention(q, q, q, is_causal=True)
    return "flash / memory-efficient attention available"


check("attention (SDPA)", sdpa)


def bnb4():
    import bitsandbytes as bnb
    layer = bnb.nn.Linear4bit(1024, 1024, compute_dtype=torch.bfloat16, quant_type="nf4").cuda()
    y = layer(torch.randn(4, 1024, device="cuda", dtype=torch.bfloat16))
    return f"{bnb.__version__}, NF4 Linear on GPU -> {tuple(y.shape)}"


try:
    importlib.import_module("bitsandbytes")
    check("bitsandbytes 4-bit", bnb4)
except ImportError:
    pass

for mod in ("transformers", "peft", "trl", "accelerate", "datasets", "langchain", "langgraph", "llama_index.core",
            "sentence_transformers", "unsloth"):
    try:
        m = importlib.import_module(mod)
        if getattr(m, "__file__", None) is None:
            continue  # a folder with that name (namespace package), not the installed library
        print(f"OK   import {mod} {getattr(m, '__version__', '')}")
    except ImportError:
        pass  # not part of this environment
    except Exception as exc:
        ok = False
        print(f"FAIL import {mod}: {type(exc).__name__}: {str(exc).splitlines()[0][:160]}")

print("RESULT", "ok" if ok else "FAIL")
sys.exit(0 if ok else 1)
