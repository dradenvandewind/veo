// Package complexity analyzes per-frame and per-segment content complexity
// for driving adaptive encoding parameters. Extracts spatial complexity
// (texture/detail via entropy), temporal complexity (motion via frame
// differences), interlacing ratio, film content detection, and temporal
// consistency using FFmpeg filters.
package complexity

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/terranvigil/veo/internal/ffmpeg"
)

type FrameComplexity struct {
	PTS                 time.Duration `json:"pts"`
	Spatial             float64       `json:"spatial"`              // normalized entropy (0-1, higher = more complex)
	Temporal            float64       `json:"temporal"`             // inter-frame luma difference (0-255)
	DCTEnergy           float64       `json:"dct_energy"`           // average DCT coefficient energy (higher = more texture)
	InterlaceScore      float64       `json:"interlace_score"`      // 0-1, ratio of interlaced field detections (from idet)
	TemporalConsistency float64       `json:"temporal_consistency"` // 0-1, 1 = highly consistent (from tblend)
}

type SegmentComplexity struct {
	Start               time.Duration `json:"start"`
	End                 time.Duration `json:"end"`
	Duration            time.Duration `json:"duration"`
	AvgSpatial          float64       `json:"avg_spatial"`
	AvgTemporal         float64       `json:"avg_temporal"`
	MaxSpatial          float64       `json:"max_spatial"`
	MaxTemporal         float64       `json:"max_temporal"`
	AvgInterlaceScore   float64       `json:"avg_interlace_score"`
	TemporalConsistency float64       `json:"temporal_consistency"`
	SceneChanges        int           `json:"scene_changes"`
	Score               float64       `json:"score"` // combined 0-100 complexity score
}

type Profile struct {
	Frames              []FrameComplexity   `json:"frames"`
	Segments            []SegmentComplexity `json:"segments"`
	AvgSpatial          float64             `json:"avg_spatial"`
	AvgTemporal         float64             `json:"avg_temporal"`
	AvgMotionVariance   float64             `json:"avg_motion_variance"`
	AvgInterlaceScore   float64             `json:"avg_interlace_score"`
	TemporalConsistency float64             `json:"temporal_consistency"`
	HasFilmContent      bool                `json:"has_film_content"`
	TotalSceneChanges   int                 `json:"total_scene_changes"`
	OverallScore        float64             `json:"overall_score"`
}

type AnalyzeOpts struct {
	// SegmentDuration is the duration of each analysis segment.
	// Default: 2 seconds (aligned with typical GOP/segment duration)
	SegmentDuration time.Duration

	// Subsample analyzes every Nth frame (1 = every frame).
	// Default: 1
	Subsample int

	// SampleDuration caps how many seconds are read for auxiliary passes
	// (interlacing, film detection, temporal consistency).
	// Default: 30 seconds (matches Python heuristic)
	SampleDuration time.Duration
}

// DefaultOpts returns sensible defaults.
func DefaultOpts() AnalyzeOpts {
	return AnalyzeOpts{
		SegmentDuration: 2 * time.Second,
		Subsample:       1,
		SampleDuration:  30 * time.Second,
	}
}

// auxiliaryMetrics holds results from the three parallel auxiliary passes.
type auxiliaryMetrics struct {
	interlaceFrames     []interlaceFrame // per-frame idet scores
	hasFilmContent      bool
	temporalConsistency float64 // 0-1
	totalSceneChanges   int
	motionVariance      float64
}

// interlaceFrame holds the idet result for one frame.
type interlaceFrame struct {
	PTS   time.Duration
	Score float64 // 0-1
}

// Analyze extracts per-frame complexity metrics and aggregates them into segments.
// It runs the core entropy+signalstats pass and three auxiliary passes concurrently.
func Analyze(ctx context.Context, path string, opts AnalyzeOpts) (*Profile, error) {
	if opts.SegmentDuration <= 0 {
		opts.SegmentDuration = 2 * time.Second
	}
	if opts.Subsample <= 0 {
		opts.Subsample = 1
	}
	if opts.SampleDuration <= 0 {
		opts.SampleDuration = 30 * time.Second
	}

	// Probe for total duration.
	probe, err := ffmpeg.Probe(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("probe failed: %w", err)
	}
	totalDuration := time.Duration(probe.Format.Duration * float64(time.Second))

	// --- Main pass: entropy + signalstats ---
	selectFilter := ""
	if opts.Subsample > 1 {
		selectFilter = fmt.Sprintf("select='not(mod(n\\,%d))',", opts.Subsample)
	}
	filter := fmt.Sprintf("%sentropy,signalstats,metadata=mode=print:file=-", selectFilter)

	mainOut, err := runFFmpegFilter(ctx, path, filter, 0)
	if err != nil {
		return nil, fmt.Errorf("complexity analysis failed: %w", err)
	}
	frames := parseComplexityOutput(mainOut)
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames analyzed")
	}

	// --- Auxiliary passes (concurrent) ---
	aux, err := runAuxiliaryPasses(ctx, path, opts.SampleDuration)
	if err != nil {
		// Non-fatal: log and proceed with zero values.
		aux = &auxiliaryMetrics{temporalConsistency: 0.5}
	}

	// Merge per-frame idet scores into frames by nearest PTS.
	mergeInterlaceScores(frames, aux.interlaceFrames)

	// Merge temporal consistency (global value broadcast to all frames).
	for i := range frames {
		frames[i].TemporalConsistency = aux.temporalConsistency
	}

	// Aggregate into segments.
	segments := aggregateSegments(frames, totalDuration, opts.SegmentDuration)

	// Overall stats.
	var totalSpatial, totalTemporal, totalInterlace float64
	for _, f := range frames {
		totalSpatial += f.Spatial
		totalTemporal += f.Temporal
		totalInterlace += f.InterlaceScore
	}
	n := float64(len(frames))
	avgSpatial := totalSpatial / n
	avgTemporal := totalTemporal / n
	avgInterlace := totalInterlace / n
	overallScore := computeScore(avgSpatial, avgTemporal, avgInterlace, aux.temporalConsistency)

	return &Profile{
		Frames:              frames,
		Segments:            segments,
		AvgSpatial:          avgSpatial,
		AvgTemporal:         avgTemporal,
		AvgMotionVariance:   aux.motionVariance,
		AvgInterlaceScore:   avgInterlace,
		TemporalConsistency: aux.temporalConsistency,
		HasFilmContent:      aux.hasFilmContent,
		TotalSceneChanges:   aux.totalSceneChanges,
		OverallScore:        overallScore,
	}, nil
}

// runAuxiliaryPasses executes three FFmpeg passes concurrently:
//  1. idet → interlacing ratio + scene changes (via motion spike)
//  2. pullup+idet → film/telecine detection
//  3. tblend=difference → temporal consistency
func runAuxiliaryPasses(ctx context.Context, path string, sampleDur time.Duration) (*auxiliaryMetrics, error) {
	type result struct {
		aux *auxiliaryMetrics
		err error
	}

	ch := make(chan result, 3)
	var wg sync.WaitGroup

	// Pass 1: idet for interlacing + scene change heuristic.
	wg.Add(1)
	go func() {
		defer wg.Done()
		frames, sceneChanges, motionVar, err := runIdetPass(ctx, path, sampleDur)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{aux: &auxiliaryMetrics{
			interlaceFrames:   frames,
			totalSceneChanges: sceneChanges,
			motionVariance:    motionVar,
		}}
	}()

	// Pass 2: pullup+idet for film/telecine content.
	wg.Add(1)
	go func() {
		defer wg.Done()
		isFilm, err := runFilmDetectPass(ctx, path, sampleDur)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{aux: &auxiliaryMetrics{hasFilmContent: isFilm}}
	}()

	// Pass 3: tblend for temporal consistency.
	wg.Add(1)
	go func() {
		defer wg.Done()
		tc, err := runTemporalConsistencyPass(ctx, path, sampleDur)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{aux: &auxiliaryMetrics{temporalConsistency: tc}}
	}()

	go func() {
		wg.Wait()
		close(ch)
	}()

	merged := &auxiliaryMetrics{temporalConsistency: 0.5}
	for r := range ch {
		if r.err != nil {
			continue // non-fatal
		}
		if r.aux.interlaceFrames != nil {
			merged.interlaceFrames = r.aux.interlaceFrames
			merged.totalSceneChanges = r.aux.totalSceneChanges
			merged.motionVariance = r.aux.motionVariance
		}
		if r.aux.hasFilmContent {
			merged.hasFilmContent = true
		}
		if r.aux.temporalConsistency != 0 {
			merged.temporalConsistency = r.aux.temporalConsistency
		}
	}

	return merged, nil
}

// runIdetPass uses the idet filter to detect interlaced frames.
// It also estimates scene changes from single-frame mode detections spikes.
//
// idet outputs per-frame lines like:
//
//	[Parsed_idet_0 @ ...] Single frame detection: TFF:3 BFF:0 Progressive:22 Undetermined:0
func runIdetPass(ctx context.Context, path string, sampleDur time.Duration) ([]interlaceFrame, int, float64, error) {
	out, err := runFFmpegFilter(ctx, path, "idet", sampleDur)
	if err != nil {
		return nil, 0, 0, err
	}

	var frames []interlaceFrame
	var sceneChanges int
	var interlacedCounts []float64

	for line := range scanLines(out) {
		if !strings.Contains(line, "Single frame detection") {
			continue
		}
		tff := parseIdetField(line, "TFF:")
		bff := parseIdetField(line, "BFF:")
		prog := parseIdetField(line, "Progressive:")
		undet := parseIdetField(line, "Undetermined:")

		total := tff + bff + prog + undet
		if total == 0 {
			continue
		}
		interlacedRatio := float64(tff+bff) / float64(total)
		interlacedCounts = append(interlacedCounts, interlacedRatio)

		// Heuristic: a sudden spike to >0.8 after a near-0 run = scene change.
		if len(interlacedCounts) > 1 {
			prev := interlacedCounts[len(interlacedCounts)-2]
			if math.Abs(interlacedRatio-prev) > 0.6 {
				sceneChanges++
			}
		}

		frames = append(frames, interlaceFrame{
			PTS:   time.Duration(len(frames)) * 40 * time.Millisecond, // rough 25fps estimate
			Score: interlacedRatio,
		})
	}

	// Motion variance = variance of interlace-score series (proxy for field-order instability).
	motionVar := variance(interlacedCounts)

	return frames, sceneChanges, motionVar, nil
}

// runFilmDetectPass uses pullup+idet to detect 3:2/2:2 telecine patterns.
// If >20% of frames are flagged as repeated (pullup removes them), the content
// is likely film. Mirrors the Python _detect_film_content heuristic.
func runFilmDetectPass(ctx context.Context, path string, sampleDur time.Duration) (bool, error) {
	out, err := runFFmpegFilter(ctx, path, "pullup,idet", sampleDur)
	if err != nil {
		return false, err
	}

	var repeated int
	for line := range scanLines(out) {
		if strings.Contains(strings.ToLower(line), "repeated") {
			repeated++
		}
	}

	sampleSeconds := sampleDur.Seconds()
	totalFrames := sampleSeconds * 25 // assume 25 fps
	return float64(repeated)/math.Max(totalFrames, 1) > 0.2, nil
}

// runTemporalConsistencyPass uses tblend=all_mode=difference to measure
// frame-to-frame luma change variance. Low variance → high consistency (→ 1.0).
// Mirrors the Python _analyze_temporal_consistency heuristic.
func runTemporalConsistencyPass(ctx context.Context, path string, sampleDur time.Duration) (float64, error) {
	out, err := runFFmpegFilter(ctx, path,
		"tblend=all_mode=difference,metadata=mode=print:file=-", sampleDur)
	if err != nil {
		return 0.5, err
	}

	var diffs []float64
	for line := range scanLines(out) {
		if !strings.HasPrefix(line, "lavfi.tblend") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err == nil {
			diffs = append(diffs, v)
		}
	}

	if len(diffs) == 0 {
		return 0.5, nil
	}
	// 1 - clamped normalised variance; low variance = high consistency.
	return 1.0 - math.Min(variance(diffs)/1000.0, 1.0), nil
}

// mergeInterlaceScores assigns each idet frame score to the main frame with
// the nearest PTS. Falls back to zero if no idet data is available.
func mergeInterlaceScores(frames []FrameComplexity, idetFrames []interlaceFrame) {
	if len(idetFrames) == 0 {
		return
	}
	j := 0
	for i := range frames {
		// Advance j while the next idet frame is closer in PTS.
		for j+1 < len(idetFrames) {
			d0 := absDur(frames[i].PTS - idetFrames[j].PTS)
			d1 := absDur(frames[i].PTS - idetFrames[j+1].PTS)
			if d1 < d0 {
				j++
			} else {
				break
			}
		}
		frames[i].InterlaceScore = idetFrames[j].Score
	}
}

// runFFmpegFilter is a helper that runs an FFmpeg null-sink pass with the given
// vf filter chain, optionally capped to sampleDur (0 = no cap), and returns
// the combined stdout+stderr output as a string.
func runFFmpegFilter(ctx context.Context, path, filter string, sampleDur time.Duration) (string, error) {
	args := []string{"-i", path}
	if sampleDur > 0 {
		args = append(args, "-t", fmt.Sprintf("%.0f", sampleDur.Seconds()))
	}
	args = append(args, "-vf", filter, "-f", "null", "-")

	cmd := exec.CommandContext(ctx, ffmpeg.FFmpegPath(), args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf // idet/tblend write to stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg(%s): %w", filter, err)
	}
	return buf.String(), nil
}

// parses entropy + signalstats metadata lines from FFmpeg stdout.
func parseComplexityOutput(output string) []FrameComplexity {
	var frames []FrameComplexity
	var current FrameComplexity
	hasPTS := false

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "frame:") {
			if hasPTS {
				frames = append(frames, current)
			}
			current = FrameComplexity{}
			hasPTS = false

			if ptsTime := extractField(line, "pts_time:"); ptsTime != "" {
				if seconds, err := strconv.ParseFloat(ptsTime, 64); err == nil {
					current.PTS = time.Duration(seconds * float64(time.Second))
					hasPTS = true
				}
			}
			continue
		}

		if strings.HasPrefix(line, "lavfi.entropy.normalized_entropy.normal.Y=") {
			val := strings.TrimPrefix(line, "lavfi.entropy.normalized_entropy.normal.Y=")
			current.Spatial, _ = strconv.ParseFloat(val, 64)
		}

		if strings.HasPrefix(line, "lavfi.signalstats.YDIF=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YDIF=")
			current.Temporal, _ = strconv.ParseFloat(val, 64)
		}

		// Luma range (YHIGH - YLOW) as proxy for DCT energy / texture complexity.
		if strings.HasPrefix(line, "lavfi.signalstats.YHIGH=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YHIGH=")
			yHigh, _ := strconv.ParseFloat(val, 64)
			current.DCTEnergy = yHigh
		}
		if strings.HasPrefix(line, "lavfi.signalstats.YLOW=") {
			val := strings.TrimPrefix(line, "lavfi.signalstats.YLOW=")
			yLow, _ := strconv.ParseFloat(val, 64)
			current.DCTEnergy -= yLow
			if current.DCTEnergy < 0 {
				current.DCTEnergy = 0
			}
		}
	}

	if hasPTS {
		frames = append(frames, current)
	}
	return frames
}

// groups frames into fixed-duration segments and computes per-segment scores.
func aggregateSegments(frames []FrameComplexity, totalDuration, segDuration time.Duration) []SegmentComplexity {
	if len(frames) == 0 {
		return nil
	}

	var segments []SegmentComplexity
	segStart := time.Duration(0)

	for segStart < totalDuration {
		segEnd := segStart + segDuration
		if segEnd > totalDuration {
			segEnd = totalDuration
		}

		var spatial, temporal, dctEnergy, interlace, consistency []float64
		var sceneChanges int

		for i, f := range frames {
			if f.PTS < segStart || f.PTS >= segEnd {
				continue
			}
			spatial = append(spatial, f.Spatial)
			temporal = append(temporal, f.Temporal)
			dctEnergy = append(dctEnergy, f.DCTEnergy)
			interlace = append(interlace, f.InterlaceScore)
			consistency = append(consistency, f.TemporalConsistency)

			// Scene change heuristic: sharp jump in spatial entropy between adjacent frames.
			if i > 0 && math.Abs(f.Spatial-frames[i-1].Spatial) > 0.1 {
				sceneChanges++
			}
		}

		if len(spatial) > 0 {
			avgDCT := mean(dctEnergy)
			seg := SegmentComplexity{
				Start:               segStart,
				End:                 segEnd,
				Duration:            segEnd - segStart,
				AvgSpatial:          mean(spatial),
				AvgTemporal:         mean(temporal),
				MaxSpatial:          maxSlice(spatial),
				MaxTemporal:         maxSlice(temporal),
				AvgInterlaceScore:   mean(interlace),
				TemporalConsistency: mean(consistency),
				SceneChanges:        sceneChanges,
			}
			seg.Score = computeScoreWithDCT(
				seg.AvgSpatial, seg.AvgTemporal, avgDCT,
				seg.AvgInterlaceScore, seg.TemporalConsistency,
				seg.SceneChanges, seg.Duration.Seconds(),
			)
			segments = append(segments, seg)
		}

		segStart = segEnd
	}

	return segments
}

// computeScore produces a 0-100 complexity score from entropy, motion,
// interlacing ratio, and temporal consistency.
// Spatial complexity has more impact on encoding than temporal.
// Interlacing and low temporal consistency both increase effective complexity.
func computeScore(spatial, temporal, interlaceScore, temporalConsistency float64) float64 {
	spatialNorm := math.Min(100, math.Max(0, (spatial-0.5)*200))
	temporalNorm := math.Min(100, temporal*3.33)
	// Interlaced content is harder to encode: boost score proportionally.
	interlaceBoost := interlaceScore * 20
	// Low temporal consistency (high scene churn) increases complexity.
	consistencyPenalty := (1.0 - temporalConsistency) * 15
	return spatialNorm*0.6 + temporalNorm*0.4 + interlaceBoost + consistencyPenalty
}

// computeScoreWithDCT incorporates DCT energy alongside entropy, motion,
// interlacing ratio, temporal consistency, and scene change density.
func computeScoreWithDCT(spatial, temporal, dctEnergy, interlaceScore, temporalConsistency float64, sceneChanges int, segmentDurationSec float64) float64 {
	spatialNorm := math.Min(100, math.Max(0, (spatial-0.5)*200))
	temporalNorm := math.Min(100, temporal*3.33)
	dctNorm := math.Min(100, dctEnergy*0.5)
	// Interlaced content requires more bits regardless of spatial complexity.
	interlaceBoost := interlaceScore * 20
	// Low temporal consistency signals erratic frame-to-frame changes.
	consistencyPenalty := (1.0 - temporalConsistency) * 15
	// Scene change density (changes per second) adds encoding overhead.
	sceneChangeDensity := 0.0
	if segmentDurationSec > 0 {
		sceneChangeDensity = math.Min(15, (float64(sceneChanges)/segmentDurationSec)*5)
	}
	base := spatialNorm*0.4 + dctNorm*0.3 + temporalNorm*0.3
	return math.Min(100, base+interlaceBoost+consistencyPenalty+sceneChangeDensity)
}

// --- helpers ---

func mean(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}

func variance(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := mean(vals)
	var sumSq float64
	for _, v := range vals {
		d := v - m
		sumSq += d * d
	}
	return sumSq / float64(len(vals))
}

func maxSlice(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func scanLines(s string) chan string {
	ch := make(chan string)
	go func() {
		scanner := bufio.NewScanner(strings.NewReader(s))
		for scanner.Scan() {
			ch <- scanner.Text()
		}
		close(ch)
	}()
	return ch
}

func parseIdetField(line, key string) int {
	idx := strings.Index(line, key)
	if idx < 0 {
		return 0
	}
	rest := strings.TrimLeft(line[idx+len(key):], " ")
	end := strings.IndexAny(rest, " \t\n")
	if end >= 0 {
		rest = rest[:end]
	}
	v, _ := strconv.Atoi(rest)
	return v
}

func extractField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(line[idx+len(key):], " ")
	end := strings.IndexAny(rest, " \t\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}