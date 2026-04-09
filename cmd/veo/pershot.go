package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/terranvigil/veo/internal/chart"
	"github.com/terranvigil/veo/internal/encoding"
	"github.com/terranvigil/veo/internal/ffmpeg"
	"github.com/terranvigil/veo/internal/hull"
	"github.com/terranvigil/veo/internal/ladder"
	"github.com/terranvigil/veo/internal/pershot"
	"github.com/terranvigil/veo/internal/shot"
)

var pershotCmd = &cobra.Command{
	Use:   "per-shot",
	Short: "Per-shot encoding optimization",
}

var (
	psInput       string
	psOutput      string
	psThreshold   float64
	psMinDur      float64
	psCodecs      []string
	psResolutions []string
	psCRFValues   []int
	psPreset      string
	psSubsample   int
	psParallel    int
	psTargetBR    float64
	ptChartDirShot    string
	ptCheckpointShot  string
)

var pershotDetectCmd = &cobra.Command{
	Use:   "detect",
	Short: "Detect shot boundaries in a video",
	RunE:  runPershotDetect,
}

var pershotAnalyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Run per-shot analysis with Trellis bit allocation",
	Long: `Detects shot boundaries, runs independent per-title analysis on each shot,
and uses Trellis optimization to allocate bits across shots for maximum quality.`,
	RunE: runPershotAnalyze,
}

func init() {
	pershotDetectCmd.Flags().StringVarP(&psInput, "input", "i", "", "source video file")
	pershotDetectCmd.Flags().Float64Var(&psThreshold, "threshold", 10.0, "scene change threshold (0-100, lower = more sensitive, default 10)")
	pershotDetectCmd.Flags().Float64Var(&psMinDur, "min-duration", 0.5, "minimum shot duration in seconds")
	pershotDetectCmd.Flags().StringVarP(&psOutput, "json-output", "j", "", "output JSON file (optional, defaults to <input>_shots.json)")
	mustMarkRequired(pershotDetectCmd, "input")

	pershotAnalyzeCmd.Flags().StringVarP(&psInput, "input", "i", "", "source video file")
	pershotAnalyzeCmd.Flags().StringVarP(&psOutput, "output", "o", "", "output JSON file (optional)")
	pershotAnalyzeCmd.Flags().Float64Var(&psThreshold, "threshold", 10.0, "scene change threshold")
	pershotAnalyzeCmd.Flags().Float64Var(&psMinDur, "min-duration", 0.5, "minimum shot duration (seconds)")
	pershotAnalyzeCmd.Flags().StringSliceVar(&psCodecs, "codecs", []string{"libx264"}, "codecs to test")
	pershotAnalyzeCmd.Flags().StringSliceVar(&psResolutions, "resolutions", []string{"360p", "480p", "720p", "1080p"}, "resolutions to test")
	pershotAnalyzeCmd.Flags().IntSliceVar(&psCRFValues, "crf-values", []int{18, 22, 26, 30, 34, 38, 42}, "CRF values to test")
	pershotAnalyzeCmd.Flags().StringVar(&psPreset, "preset", "veryfast", "encoding preset")
	pershotAnalyzeCmd.Flags().IntVar(&psSubsample, "subsample", 5, "VMAF frame subsampling")
	pershotAnalyzeCmd.Flags().IntVar(&psParallel, "parallel", 2, "max parallel encodes")
	pershotAnalyzeCmd.Flags().Float64Var(&psTargetBR, "target-bitrate", 2000, "target average bitrate (kbps) for Trellis optimization")
	mustMarkRequired(pershotAnalyzeCmd, "input")

	pershotAnalyzeCmd.Flags().StringVar(&ptChartDirShot, "charts", "", "directory to save PNG chart images (optional)")
	pershotAnalyzeCmd.Flags().StringVar(&ptCheckpointShot, "checkpoint", "", "checkpoint file for resuming analysis (optional)")

	pershotCmd.AddCommand(pershotDetectCmd)
	pershotCmd.AddCommand(pershotAnalyzeCmd)
	rootCmd.AddCommand(pershotCmd)
}

func runPershotDetect(cmd *cobra.Command, args []string) error {
    opts := shot.DetectOpts{
        Threshold:   psThreshold,
        MinDuration: durFromSeconds(psMinDur),
    }
    fmt.Printf("Detecting shots: %s (threshold=%.2f, min=%.1fs)\n\n", psInput, psThreshold, psMinDur)
    shots, err := shot.Detect(context.Background(), psInput, opts)
    if err != nil {
        return fmt.Errorf("shot detection failed: %w", err)
    }
    fmt.Printf("Found %d shots:\n\n", len(shots))

    w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
    _, _ = fmt.Fprintf(w, "  #\tStart\tEnd\tDuration\n")
    _, _ = fmt.Fprintf(w, "  -\t-----\t---\t--------\n")
    for _, s := range shots {
        _, _ = fmt.Fprintf(w, "  %d\t%.2fs\t%.2fs\t%.2fs\n",
            s.Index+1, s.Start.Seconds(), s.End.Seconds(), s.Duration.Seconds())
    }
    _ = w.Flush()

    // Build JSON output
    type Timecode struct {
        Start    float64 `json:"start"`
        Stop     float64 `json:"stop"`
        Duration float64 `json:"duration"`
    }
    type Segment struct {
        SegmentID string   `json:"segment_id"`
        Timecode  Timecode `json:"timecode"`
    }

    segments := make([]Segment, 0, len(shots))
    for _, s := range shots {
        segments = append(segments, Segment{
            SegmentID: fmt.Sprintf("seg_%04d", s.Index+1),
            Timecode: Timecode{
                Start:    s.Start.Seconds(),
                Stop:     s.End.Seconds(),
                Duration: s.Duration.Seconds(),
            },
        })
    }

    out, err := json.MarshalIndent(segments, "", "  ")
    if err != nil {
        return fmt.Errorf("json marshal failed: %w", err)
    }

    // stdout
    fmt.Printf("\n%s\n", out)

    // side-car path : flag --json-output ou fallback automatique
    sidecar := psOutput
    if sidecar == "" {
        sidecar = strings.TrimSuffix(psInput, filepath.Ext(psInput)) + "_shots.json"
    }
    if err := os.WriteFile(sidecar, out, 0644); err != nil {
        return fmt.Errorf("writing sidecar json failed: %w", err)
    }
    fmt.Printf("\nJSON written to: %s\n", sidecar)

    return nil
}


func runPershotAnalyze(cmd *cobra.Command, args []string) error {
	resolutions, err := parseResolutions(psResolutions)
	if err != nil {
		return err
	}

	codecs := make([]ffmpeg.Codec, len(psCodecs))
	for i, c := range psCodecs {
		codecs[i] = ffmpeg.Codec(c)
	}

	cfg := pershot.Config{
		Config: encoding.Config{
			Resolutions: resolutions,
			CRFValues:   psCRFValues,
			Codecs:      codecs,
			Preset:      psPreset,
			Subsample:   psSubsample,
			Parallel:    psParallel,
			RateControl: ffmpeg.RateControlMode(ptMode),
		},
		ShotOpts: shot.DetectOpts{
			Threshold:   psThreshold,
			MinDuration: durFromSeconds(psMinDur),
		},
		LadderOpts: ladder.DefaultOpts(),
		CheckpointPath: ptCheckpointShot,
	}

	fmt.Println("╔══════════════════════════════════════════╗")
	fmt.Println("║         Per-Shot Analysis                ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Println()
	fmt.Printf("  Source:      %s\n", psInput)
	fmt.Printf("  Threshold:   %.2f\n", psThreshold)
	fmt.Printf("  Codecs:      %s\n", strings.Join(psCodecs, ", "))
	fmt.Printf("  Target BR:   %.0f kbps\n", psTargetBR)
	fmt.Println()

	progress := make(chan pershot.Progress, 10)
	go func() {
		for p := range progress {
			fmt.Printf("\r  Analyzing shot %d/%d...    ", p.ShotDone, p.ShotTotal)
		}
	}()

	result, err := pershot.Analyze(context.Background(), psInput, cfg, progress)
	close(progress)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	fmt.Printf("\r  Analyzed %d shots (%d total trials) in %s\n\n",
		result.ShotCount, result.TrialCount, result.Duration.Truncate(time.Millisecond))

	// lPer-shot hulls
	for _, sr := range result.Shots {
		fmt.Printf("  Shot %d (%.1fs - %.1fs, %.1fs):\n",
			sr.Shot.Index+1, sr.Shot.Start.Seconds(), sr.Shot.End.Seconds(), sr.Shot.Duration.Seconds())
		if len(sr.Hull.Points) > 0 {
			fmt.Printf("    Hull: %d points, %.0f - %.0f kbps, VMAF %.1f - %.1f\n",
				len(sr.Hull.Points),
				sr.Hull.Points[0].Bitrate, sr.Hull.Points[len(sr.Hull.Points)-1].Bitrate,
				sr.Hull.Points[0].VMAF, sr.Hull.Points[len(sr.Hull.Points)-1].VMAF)
		}
	}

	// lTrellis optimization
	if psTargetBR > 0 && len(result.Shots) > 1 {
		fmt.Printf("\n  Trellis Optimization (target: %.0f kbps avg):\n\n", psTargetBR)
		assignments := pershot.TrellisOptimize(result.Shots, pershot.TrellisOpts{
			TargetBitrate: psTargetBR,
		})

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(w, "    Shot\tRes\tCodec\tCRF\tBitrate\tVMAF\n")
		_, _ = fmt.Fprintf(w, "    ----\t---\t-----\t---\t-------\t----\n")
		var totalWeighted float64
		var totalDur float64
		for _, a := range assignments {
			dur := result.Shots[a.ShotIndex].Shot.Duration.Seconds()
			totalWeighted += a.Bitrate * dur
			totalDur += dur
			_, _ = fmt.Fprintf(w, "    %d\t%s\t%s\t%d\t%.0f kbps\t%.1f\n",
				a.ShotIndex+1, a.Resolution.Label(), a.Codec, a.CRF, a.Bitrate, a.VMAF)
		}
		_ = w.Flush()
		if totalDur > 0 {
			fmt.Printf("\n    Weighted avg: %.0f kbps (target: %.0f)\n", totalWeighted/totalDur, psTargetBR)
		}
	}

		// lGenerate charts if requested
	if ptChartDirShot != "" {
		if err := os.MkdirAll(ptChartDirShot, 0o755); err != nil {
			return fmt.Errorf("failed to create chart directory: %w", err)
		}

		// lR-D curve with hull
		rdData, err := chart.RDCurve(result.Shots[0].Hull.Points, result.Shots[0].Hull, chart.Opts{
			Title: fmt.Sprintf("R-D Curve: %s", filepath.Base(ptInput)),
		})
		if err != nil {
			return fmt.Errorf("failed to generate R-D chart: %w", err)
		}
		rdPath := ptChartDirShot + "/rd-curve.png"
		if err := chart.SavePNG(rdData, rdPath); err != nil {
			return fmt.Errorf("failed to save R-D chart: %w", err)
		}
		fmt.Printf("\nChart: %s\n", rdPath)

		// lPer-codec comparison (if multiple codecs)
		if len(result.Shots[0].PerCodec) > 1 {
			// lCompute BD-Rate for chart annotation
			var bdRateVal float64
			codecList := make([]ffmpeg.Codec, 0, len(result.Shots[0].PerCodec))
			for c := range result.Shots[0].PerCodec {
				codecList = append(codecList, c)
			}
			if len(codecList) >= 2 {
				//bdRateVal, _ = hull.BDRate(result.Shots[0].PerCodec[codecList[0]].Points, result.Shots[0].PerCodec[codecList[1]].Points)
				c0 := result.Shots[0].PerCodec[codecList[0]]
				c1 := result.Shots[0].PerCodec[codecList[1]]
				// Guard: both hulls must have at least 2 points for BD-Rate
				if c0 != nil && c1 != nil && len(c0.Points) >= 2 && len(c1.Points) >= 2 {
					bdRateVal, _ = hull.BDRate(c0.Points, c1.Points)
				}
			}

			codecData, err := chart.PerCodecRDCurve(result.Shots[0].PerCodec, bdRateVal, chart.Opts{
				Title: fmt.Sprintf("Codec Comparison: %s", filepath.Base(ptInput)),
			})
			if err != nil {
				return fmt.Errorf("failed to generate codec chart: %w", err)
			}
			codecPath := ptChartDirShot + "/codec-comparison.png"
			if err := chart.SavePNG(codecData, codecPath); err != nil {
				return fmt.Errorf("failed to save codec chart: %w", err)
			}
			fmt.Printf("Chart: %s\n", codecPath)
		}

		// lLadder bar chart
		// Ladder bar chart
		if result.Shots[0].Ladder != nil && len(result.Shots[0].Ladder.Rungs) > 0 {
			ladderData, err := chart.LadderChart(result.Shots[0].Ladder, chart.Opts{
				Title: fmt.Sprintf("Bitrate Ladder: %s", filepath.Base(ptInput)),
			})
			if err != nil {
				return fmt.Errorf("failed to generate ladder chart: %w", err)
			}
			ladderPath := filepath.Join(ptChartDirShot, "ladder.png")
			if err := chart.SavePNG(ladderData, ladderPath); err != nil {
				return fmt.Errorf("failed to save ladder chart: %w", err)
			}
			fmt.Printf("Chart: %s\n", ladderPath)
		}
	}


	fmt.Println()
	return nil
}

func durFromSeconds(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}
