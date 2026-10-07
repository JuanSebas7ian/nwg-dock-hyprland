# Entorno de IA: deep learning, LLMs y AI engineering

Para este equipo: RTX 3060 12 GB (compute 8.6, bf16), driver NVIDIA 610 (CUDA 13.3), Python 3.12 gestionado por `uv`.
Todo es de usuario (sin root) y reproducible: las versiones exactas están en los `uv.lock`.

```bash
contrib/omarchy/ai/install.sh            # crea o repara ~/ai y ~/ai/unsloth-env, registra los kernels y verifica la GPU
contrib/omarchy/ai/install.sh --relock   # actualiza a las versiones nuevas (uv lock --upgrade); haz commit de los uv.lock
~/ai/.venv/bin/python ~/ai/verify.py     # comprobación rápida en cualquier momento
```

## Qué hay

| Entorno | Dónde | Kernel de Jupyter | Contenido |
|---|---|---|---|
| **ai-lab** | `~/ai/.venv` | AI Lab (CUDA) | PyTorch 2.14 + CUDA 13.2, torchvision, Lightning, timm, ONNX; transformers, datasets, accelerate, PEFT, TRL, bitsandbytes, sentence-transformers, `hf` CLI; LangChain (+ Ollama/OpenAI/Anthropic), LangGraph, LlamaIndex, LiteLLM, Chroma, cliente Qdrant, FastAPI, Gradio, Streamlit; numpy, pandas, polars, DuckDB, scikit-learn, librosa; TensorBoard, MLflow, W&B |
| **unsloth** | `~/ai/unsloth-env/.venv` | Unsloth (CUDA) | Unsloth (fine-tuning LoRA/QLoRA de LLM de 7-8B en 12 GB, ~2× más rápido). Aparte porque fija sus propias versiones de torch/trl |

Sistema (paquetes del repo): `nvtop` (GPU por proceso), `llama-cpp` + `ggml-cuda` (`llama-cli`, `llama-server`, cuantizar GGUF),
Ollama con CUDA (servicio), `nvidia-container-toolkit` (`docker run --gpus all`), CUDA 13.3 + cuDNN (para compilar extensiones).

Verificado el 2026-10-07: bf16 matmul ~15-17 TFLOPS, atención flash/eficiente (SDPA), NF4 de bitsandbytes en GPU,
LangGraph + `ChatOllama(qwen2.5:7b)` respondiendo en ~3 s.

## Uso

```bash
cd ~/ai/projects/mi-proyecto
uv init && uv add --editable ...     # proyecto propio (recomendado para algo serio: su propio uv.lock)
# o, para explorar, el entorno base:
source ~/ai/.venv/bin/activate        # o en Antigravity/Jupyter: kernel "AI Lab (CUDA)"
```

- **Proyecto nuevo con PyTorch+CUDA:** copiar el bloque `[[tool.uv.index]]` / `[tool.uv.sources]` de `pyproject.toml`
  (índice `cu132`) y `environments` de `[tool.uv]`.
- **Modelos de Hugging Face:** se guardan en `~/.cache/huggingface` (fuera del respaldo de restic). `hf auth login` para modelos privados.
- **Qué cabe en 12 GB:** inferencia de 7-8B en bf16 justo, de 13-14B en 4 bits; fine-tuning QLoRA de 7-8B con Unsloth;
  vLLM y modelos >14B no caben bien.

## Decisiones

- **Python 3.12**, no 3.14: bitsandbytes, Unsloth, vLLM y flash-attn soportan primero 3.12.
- **CUDA 13.2 (`cu132`)**: la versión de PyTorch más nueva que el driver 610 (CUDA 13.3) admite; `cu134` exigiría un driver más nuevo.
- **Sin torchaudio**: se quedó en 2.11 (modo mantenimiento, sin versión para torch 2.14). Para audio: `librosa` + `soundfile`.
- **`environments = linux x86_64`**: sin esto uv intenta resolver también para Windows/macOS y falla con los índices CUDA.
- La carpeta del segundo entorno es `unsloth-env` y no `unsloth`: con ese nombre Python la tomaba por el paquete al ejecutarse en `~/ai`.
- `.venv` ya está excluido del respaldo de restic: se reconstruye con `install.sh` (la caché de uv en `~/.cache/uv` lo hace en segundos).
