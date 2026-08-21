#!/usr/bin/env python3
"""Produce the golden fixtures in this directory using TensorFlow Lite's own runtime.

The inputs are generated here exactly as reference_test.go generates them, so the only difference
between the two sides is the interpreter.

    pip install ai-edge-litert          # or tensorflow, for tf.lite.Interpreter
    python3 generate_reference.py mel      ../../oww/assets/melspectrogram.tflite
    python3 generate_reference.py embedding ../../oww/assets/embedding_model.tflite
    python3 generate_reference.py classifier /path/to/Jarvis.tflite   # named after the model
"""

import math
import os
import sys

import numpy as np

try:
    from ai_edge_litert.interpreter import Interpreter
except ImportError:
    from tensorflow.lite import Interpreter


def signal(n):
    """referenceSignal: two tones under a slow amplitude sweep, at int16 magnitudes."""
    out = np.empty(n, dtype=np.float32)
    for i in range(n):
        t = i / 16000
        env = 0.4 + 0.6 * math.sin(2 * math.pi * 3 * t)
        v = env * (6000 * math.sin(2 * math.pi * 440 * t) + 2500 * math.sin(2 * math.pi * 1750 * t))
        out[i] = np.int16(v)
    return out


def mel_frames(n):
    """referenceMelFrames."""
    out = np.empty(n, dtype=np.float32)
    for i in range(n):
        out[i] = 6 + 4 * math.sin(i * 0.037) + 3 * math.cos(i * 0.011) - 2 * math.sin(i * 0.9)
    return out


def embeddings():
    """referenceEmbeddings: 16 frames of 96."""
    out = np.empty(16 * 96, dtype=np.float32)
    for i in range(16 * 96):
        out[i] = 0.6 * math.sin(i * 0.037) + 0.4 * math.cos(i * 0.011) - 0.3 * math.sin(i * 0.9)
    return out


# Input shapes match reference_test.go's ResizeInput calls; None leaves the model's own.
KINDS = {
    "mel": (lambda: signal(1760), [1, 1760], "mel_reference.txt"),
    "embedding": (lambda: mel_frames(76 * 32), [1, 76, 32, 1], "embedding_reference.txt"),
    "classifier": (embeddings, None, None),  # named after the model
}


def main():
    kind, model = sys.argv[1], sys.argv[2]
    make, resize, out_name = KINDS[kind]
    if out_name is None:
        stem = os.path.basename(model).removesuffix(".tflite")
        out_name = f"{kind}_{stem}_reference.txt"

    interpreter = Interpreter(model_path=model)
    spec = interpreter.get_input_details()[0]
    if resize:
        interpreter.resize_tensor_input(spec["index"], resize)
    interpreter.allocate_tensors()

    spec = interpreter.get_input_details()[0]
    data = make().reshape(spec["shape"])
    interpreter.set_tensor(spec["index"], data)
    interpreter.invoke()

    out_spec = interpreter.get_output_details()[0]
    values = interpreter.get_tensor(out_spec["index"]).ravel()

    with open(out_name, "w") as f:
        f.write(f"# reference tflite output, {kind} of {model}, shape {list(out_spec['shape'])}\n")
        for v in values:
            f.write(f"{v:.8g}\n")
    print(f"wrote {out_name}: {len(values)} values, first {values[:4]}")


if __name__ == "__main__":
    main()
