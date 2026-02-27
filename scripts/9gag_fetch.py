#!/usr/bin/env python3
"""Fetch 9GAG post data using curl_cffi browser impersonation to bypass Cloudflare."""

import json
import sys

try:
    from curl_cffi import requests
except ImportError:
    print(json.dumps({"error": "curl_cffi not installed. Run: pip install curl_cffi"}), file=sys.stdout)
    sys.exit(1)


def fetch_post(post_id):
    """Fetch a 9GAG post by ID using browser impersonation."""
    try:
        r = requests.get(
            f"https://9gag.com/v1/post?id={post_id}",
            impersonate="chrome",
            timeout=15,
        )

        if r.status_code != 200:
            return {"error": f"HTTP {r.status_code}"}

        data = r.json()
        post = data.get("data", {}).get("post", {})

        if not post:
            return {"error": "Post not found in response"}

        result = {
            "id": post.get("id", ""),
            "title": post.get("title", ""),
            "type": post.get("type", ""),
            "nsfw": post.get("nsfw", 0),
            "images": {},
        }

        for key, img in post.get("images", {}).items():
            result["images"][key] = {
                "url": img.get("url", ""),
                "width": img.get("width", 0),
                "height": img.get("height", 0),
            }
            if "vp9Url" in img:
                result["images"][key]["vp9Url"] = img["vp9Url"]
            if "h265Url" in img:
                result["images"][key]["h265Url"] = img["h265Url"]
            if "duration" in img:
                result["images"][key]["duration"] = img["duration"]
            if "hasAudio" in img:
                result["images"][key]["hasAudio"] = img["hasAudio"]

        return result

    except Exception as e:
        return {"error": str(e)}


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(json.dumps({"error": "Usage: 9gag_fetch.py <post_id>"}))
        sys.exit(1)

    post_id = sys.argv[1]
    result = fetch_post(post_id)
    print(json.dumps(result))
