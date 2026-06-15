"""Extract logit_scale and logit_bias from SigLIP2 checkpoint."""
import torch
from transformers import AutoModel

model_id = 'google/siglip2-so400m-patch16-512'
model = AutoModel.from_pretrained(model_id, dtype=torch.float32)

logit_scale = model.logit_scale.item()   # raw parameter ~4.70
logit_bias = model.logit_bias.item()     # ~ -15.93
logit_temperature = 1.0 / logit_scale    # ~0.213

params = {
    'logit_scale': logit_scale,
    'logit_bias': logit_bias,
    'logit_temperature': logit_temperature,
    'model_id': model_id,
}

output_path = 'models/siglip2-512/logit_params.txt'
with open(output_path, 'w') as f:
    for k, v in params.items():
        f.write(f'{k}={v}\n')
    f.write(f'\n# Usage in Go classifier:\n')
    f.write(f'# similarity = cosine_sim(image_embeds, text_embeds)\n')
    f.write(f'# logits = logit_scale * similarity + logit_bias\n')
    f.write(f'# probability = sigmoid(logits)\n')

print(f'Saved to {output_path}')
for k, v in params.items():
    print(f'  {k}: {v}')
