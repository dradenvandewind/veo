import subprocess
import itertools
import os
import shutil

TIMEOUT_SECONDS = 7000
# List of input files
inputs = [
    "assets/hd/crowd_run_1080p50.y4m",
    "assets/sd/akiyo_cif.y4m", 
]

presets = ["ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"]
resolutions = ["720p", "1080p"]
codecs = ["libx264", "libx265", "libsvtav1", "libvvenc"]

total = len(inputs) * len(presets) * len(resolutions) * len(codecs)
print(f"Total combinaisons : {total}")

for i, (input_file, preset, resolution, codec) in enumerate(
    itertools.product(inputs, presets, resolutions, codecs), start=1
):
    input_basename = os.path.splitext(os.path.basename(input_file))[0]
    final_output_name = f"{input_basename}_{codec}_{resolution}_{preset}.json"
    
    # Backup folder for charts
    # Format: backup_charts/filename/codec_resolution_preset
    charts_backup_dir = os.path.join("charts_backups", input_basename, f"{codec}_{resolution}_{preset}")
    #./veo context-aware analyze -i video.y4m --devices mobile,desktop,tv
    
    cmd = [
        "./veo",
        "context-aware", "analyze",
        "-i", input_file,
        "--devices", "mobile,desktop,tv",
    ]

    print(f"[{i}/{total}] Analysis: {codec} | {preset} | {resolution} ← {input_basename}")

    try:
        subprocess.run(cmd, check=True, timeout=TIMEOUT_SECONDS)
        
        # 1. Save the results JSON
        if os.path.exists("results.json"):
            shutil.move("results.json", final_output_name)
            print(f"  ✓ JSON Saved: {final_output_name}")
        
        # 2. Backup the charts directory
        if os.path.exists("./charts") and os.listdir("./charts"):
            # Create parent folder if it doesn't exist
            os.makedirs(charts_backup_dir, exist_ok=True)
            
            # Move the contents of ./charts to the backup folder
            for item in os.listdir("./charts"):
                s = os.path.join("./charts", item)
                d = os.path.join(charts_backup_dir, item)
                # Use copy2 + remove or move depending on whether it's a file or folder
                shutil.move(s, d)
            
            print(f"  ✓ Charts archived in: {charts_backup_dir}")
        else:
            print(f"  ⚠ Warning: No charts found in ./charts")

    except subprocess.TimeoutExpired:
        print(f"  ✗ TIMEOUT: {input_basename} stopped after {TIMEOUT_SECONDS}s.")
        if os.path.exists("results.json"): os.remove("results.json")
            
    except subprocess.CalledProcessError as e:
        print(f"  ✗ VEO ERROR: Code {e.returncode}")
        
    except Exception as e:
        print(f"  ✗ UNEXPECTED ERROR: {e}")

print("\n--- All analyses are complete ---")