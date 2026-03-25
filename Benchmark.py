import subprocess
import itertools
from pathlib import Path

AWCY = "assets/awcy/objective-1-fast/objective-1-fast"

inputs = [
    # SD
    "assets/sd/akiyo_cif.y4m",
    "assets/sd/foreman_cif.y4m",
    "assets/sd/mobile_cif.y4m",
    # AWCY objective-1-fast
    f"{AWCY}/aspen_1080p_60f.y4m",
    f"{AWCY}/blue_sky_360p_60f.y4m",
    f"{AWCY}/dark720p_60f.y4m",
    f"{AWCY}/DOTA2_60f_420.y4m",
    f"{AWCY}/ducks_take_off_1080p50_60f.y4m",
    f"{AWCY}/gipsrestat720p_60f.y4m",
    f"{AWCY}/kirland360p_60f.y4m",
    f"{AWCY}/KristenAndSara_1280x720_60f.y4m",
    f"{AWCY}/life_1080p30_60f.y4m",
    f"{AWCY}/MINECRAFT_60f_420.y4m",
    f"{AWCY}/Netflix_Aerial_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_Boat_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_Crosswalk_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_DrivingPOV_1280x720_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_FoodMarket_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_PierSeaside_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_RollerCoaster_1280x720_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_SquareAndTimelapse_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/Netflix_TunnelFlag_1920x1080_60fps_8bit_420_60f.y4m",
    f"{AWCY}/niklas360p_60f.y4m",
    f"{AWCY}/red_kayak_360p_60f.y4m",
    f"{AWCY}/rush_hour_1080p25_60f.y4m",
    f"{AWCY}/shields_640x360_60f.y4m",
    f"{AWCY}/speed_bag_640x360_60f.y4m",
    f"{AWCY}/STARCRAFT_60f_420.y4m",
    f"{AWCY}/thaloundeskmtg360p_60f.y4m",
    f"{AWCY}/touchdown_pass_1080p_60f.y4m",
    f"{AWCY}/vidyo1_720p_60fps_60f.y4m",
    f"{AWCY}/vidyo4_720p_60fps_60f.y4m",
    f"{AWCY}/wikipedia_420.y4m",
]

presets = [
    "ultrafast",
    "superfast",
    "veryfast",
    "faster",
    "fast",
    "medium",
    "slow",
    "slower",
    "veryslow",
]

resolutions = ["240p", "360p", "480p", "720p", "1080p"]
codecs = ["libx264", "libx265", "libsvtav1", "libvvenc", "libxeve"]

# Vérification des fichiers manquants avant de lancer
missing = [f for f in inputs if not Path(f).exists()]
if missing:
    print(f"AVERTISSEMENT : {len(missing)} fichier(s) introuvable(s) :")
    for f in missing:
        print(f"  ✗ {f}")
    print()

inputs = [f for f in inputs if Path(f).exists()]

total = len(inputs) * len(presets) * len(resolutions) * len(codecs)
print(f"Combinaisons : {total}  ({len(inputs)} fichiers × {len(presets)} presets × {len(resolutions)} résolutions × {len(codecs)} codecs)\n")

errors = []

for i, (input_file, preset, resolution, codec) in enumerate(
    itertools.product(inputs, presets, resolutions, codecs), start=1
):
    cmd = [
        "./veo",
        "per-title", "analyze",
        "-i", input_file,
        "--resolutions", resolution,
        "--codecs", codec,
        "--preset", preset,
        "--charts", "./charts",
        "-o", "results.json",
    ]

    label = f"[{i}/{total}] {codec} {preset} {resolution} ← {Path(input_file).name}"
    print(label)

    try:
        subprocess.run(cmd, check=True)
    except subprocess.CalledProcessError as e:
        msg = f"  ✗ {label} → {e}"
        print(msg)
        errors.append(msg)

print(f"\nTerminé. {len(errors)} erreur(s) sur {total} combinaisons.")
for e in errors:
    print(e)