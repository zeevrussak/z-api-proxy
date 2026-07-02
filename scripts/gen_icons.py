#!/usr/bin/env python3
"""Generate high-resolution multi-size icons for z-api-proxy."""

from PIL import Image, ImageDraw, ImageFilter
import math
import os


def create_gradient(size, color_top, color_bottom):
    """Create a vertical gradient image."""
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    pixels = img.load()
    for y in range(size):
        t = y / max(size - 1, 1)
        r = int(color_top[0] + (color_bottom[0] - color_top[0]) * t)
        g = int(color_top[1] + (color_bottom[1] - color_top[1]) * t)
        b = int(color_top[2] + (color_bottom[2] - color_top[2]) * t)
        for x in range(size):
            pixels[x, y] = (r, g, b, 255)
    return img


def rounded_rect_mask(size, radius):
    """Create a mask for a rounded rectangle."""
    mask = Image.new("L", (size, size), 0)
    draw = ImageDraw.Draw(mask)
    draw.rounded_rectangle([0, 0, size - 1, size - 1], radius=radius, fill=255)
    return mask


def draw_z_symbol(img, size, color):
    """Draw a stylized 'Z' with proxy connection dots."""
    draw = ImageDraw.Draw(img)
    s = size
    margin = s * 0.26
    line_w = max(int(s * 0.075), 2)

    # Z shape: top bar, diagonal, bottom bar
    top_y = margin
    bot_y = s - margin
    left_x = margin
    right_x = s - margin

    # Top bar (left to right)
    draw.line([(left_x, top_y), (right_x, top_y)], fill=color, width=line_w)

    # Bottom bar (left to right)
    draw.line([(left_x, bot_y), (right_x, bot_y)], fill=color, width=line_w)

    # Diagonal from top-right to bottom-left
    draw.line([(right_x, top_y), (left_x, bot_y)], fill=color, width=line_w)

    # Connection nodes at corners (proxy motif)
    dot_r = max(int(s * 0.045), 2)
    for cx, cy in [(left_x, top_y), (right_x, top_y), (left_x, bot_y), (right_x, bot_y)]:
        draw.ellipse(
            [cx - dot_r, cy - dot_r, cx + dot_r, cy + dot_r],
            fill=color,
        )

    return img


def create_icon(size, gradient_colors, symbol_color):
    """Create a single icon at the given size."""
    # Work at 4x supersampling for smooth edges, then downscale
    ss = max(size * 4, 64)
    radius = int(ss * 0.22)

    # Gradient background
    img = create_gradient(ss, gradient_colors[0], gradient_colors[1])

    # Apply rounded rect mask
    mask = rounded_rect_mask(ss, radius)
    bg = Image.new("RGBA", (ss, ss), (0, 0, 0, 0))
    bg.paste(img, (0, 0), mask)

    # Draw Z symbol
    draw_z_symbol(bg, ss, symbol_color)

    # Downscale to target size with high-quality resampling
    if size != ss:
        bg = bg.resize((size, size), Image.LANCZOS)

    return bg


def generate_icon_set(name, gradient_colors, symbol_color):
    """Generate a multi-resolution .ico file."""
    sizes = [256, 48, 32, 16]
    images = [create_icon(s, gradient_colors, symbol_color) for s in sizes]

    out_path = os.path.join("assets", f"{name}.ico")
    images[0].save(out_path, format="ICO", sizes=[(s, s) for s in sizes], append_images=images[1:])
    print(f"  {out_path}: {sizes}")

    # Also save PNG previews for convenience
    png_path = os.path.join("assets", f"{name}-preview.png")
    images[0].save(png_path, format="PNG")
    print(f"  {png_path}: 256x256 preview")


if __name__ == "__main__":
    os.makedirs("assets", exist_ok=True)

    # Normal icon: deep blue to teal gradient, white symbol
    print("Normal icon:")
    generate_icon_set(
        "icon",
        gradient_colors=[(30, 90, 200), (20, 160, 200)],
        symbol_color=(255, 255, 255),
    )

    # Error icon: dark red to bright red gradient, white symbol
    print("Error icon:")
    generate_icon_set(
        "icon-error",
        gradient_colors=[(180, 30, 30), (220, 60, 40)],
        symbol_color=(255, 255, 255),
    )

    print("Done.")
