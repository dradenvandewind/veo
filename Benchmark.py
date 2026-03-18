import subprocess
import itertools

# Liste des fichiers d'entrée
inputs = [
    "assets/sd/akiyo_cif.y4m",
    "assets/sd/foreman_cif.y4m"
    "assets/sd/mobile_cif.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/blue_sky_360p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/MINECRAFT_60f_420.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_SquareAndTimelapse_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/thaloundeskmtg360p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/dark720p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_Aerial_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_TunnelFlag_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/touchdown_pass_1080p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/DOTA2_60f_420.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_Boat_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/niklas360p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/vidyo1_720p_60fps_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/ducks_take_off_1080p50_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_Crosswalk_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/red_kayak_360p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/vidyo4_720p_60fps_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/gipsrestat720p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_DrivingPOV_1280x720_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/rush_hour_1080p25_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/wikipedia_420.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/kirland360p_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_FoodMarket_1920x1080_60fps_8bit_420_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/shields_640x360_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/KristenAndSara_1280x720_60f.y4m",
	"assets/awcy/objective-1-fast/objective-1-fast/Netflix_PierSeaside_1920x1080_60fps_8bit_420_60f.y4m  speed_bag_640x360_60f.y4m",
     
    # ajoute d'autres fichiers ici
]

# Liste des presets
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
    "placebo"
]

# Autres paramètres fixes
resolutions = ["240p","360p","480p","720p","1080p"]
codecs = ["libx264"]

# Génération de toutes les combinaisons
for input_file, preset, resolution, codec in itertools.product(inputs, presets, resolutions, codecs):
    
    cmd = [
        "./veo",
        "per-title",
        "analyze",
        "-i", input_file,
        "--resolutions", resolution,
        "--codecs", codec,
        "--preset", preset
    ]

    print("Running:", " ".join(cmd))

    try:
        subprocess.run(cmd, check=True)
    except subprocess.CalledProcessError as e:
        print(f"Erreur avec {input_file} + {preset}: {e}")
