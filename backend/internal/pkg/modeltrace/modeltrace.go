// Package modeltrace implements the closed-set ModelTrace numeric fingerprint.
// Scores describe similarity to the bundled candidates, not proof of identity.
//
// Ported from fingerprint.py and static/fingerprint-core.js at
// https://github.com/xqy2006/ModelTrace/tree/6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2.
// The Python implementation defines the simultaneous nuisance projection.
// The upstream MIT license is preserved in LICENSE.
package modeltrace

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
	"unicode"
)

// BankRevision pins both the scoring algorithm and the embedded unified bank.
const BankRevision = "6cd4a8ff0f3f4e49d6d6969dd2fbadfa1569fde2"

const (
	dimension        = 355
	orderedDimension = 74
)

//go:embed unified_bank.json
var bankJSON []byte

// ErrNoUsableOutputs means every response was refused, malformed or truncated.
var ErrNoUsableOutputs = errors.New("modeltrace: no usable numeric responses")

type Output struct {
	Text          string
	ExpectedCount int
}

type Challenge struct {
	Prompt        string
	ExpectedCount int
}

type Candidate struct {
	Model       string  `json:"model"`
	Probability float64 `json:"probability"`
	Family      string  `json:"family"`
}

type FamilyCandidate struct {
	Family      string  `json:"family"`
	Probability float64 `json:"probability"`
}

type Diagnostic struct {
	Index          int  `json:"index"`
	ParsedNumbers  int  `json:"parsed_numbers"`
	MinimumNumbers int  `json:"minimum_numbers"`
	Accepted       bool `json:"accepted"`
}

type Result struct {
	Prediction  string            `json:"prediction"`
	Probability float64           `json:"probability"`
	UsedOutputs int               `json:"used_outputs"`
	Candidates  []Candidate       `json:"candidates"`
	Families    []FamilyCandidate `json:"families"`
	Diagnostics []Diagnostic      `json:"diagnostics"`
}

type featureArtifact struct {
	Mean         []float64     `json:"feature_mean"`
	Scale        []float64     `json:"feature_scale"`
	Basis        [][]float64   `json:"nuisance_basis"`
	Centroids    [][]float64   `json:"centroids"`
	Environments [][][]float64 `json:"environment_centroids"`
	Weight       float64       `json:"weight"`
}

type fingerprintBank struct {
	Models []struct {
		ID     string `json:"id"`
		Family string `json:"family"`
	} `json:"models"`
	Robust struct {
		ModelOrder []string        `json:"model_order"`
		Hellinger  featureArtifact `json:"hellinger"`
		Ordered    featureArtifact `json:"ordered_blocks"`
	} `json:"robust"`
	Calibration map[string]struct {
		Beta float64 `json:"beta"`
	} `json:"calibration"`
}

var bundledBank = sync.OnceValues(func() (*fingerprintBank, error) {
	var bank fingerprintBank
	if err := json.Unmarshal(bankJSON, &bank); err != nil {
		return nil, fmt.Errorf("modeltrace: decode embedded bank: %w", err)
	}
	return &bank, nil
})

// Analyze averages accepted response-wise fused scores and applies the upstream
// temperature calibrated for one, two or at least three responses. Callers must
// require three accepted responses before treating the result as conclusive.
// When no output is usable, the returned result still contains diagnostics.
func Analyze(outputs []Output) (Result, error) {
	result := Result{Diagnostics: make([]Diagnostic, 0, len(outputs))}
	bank, err := bundledBank()
	if err != nil {
		return result, err
	}
	modelCount := len(bank.Models)
	combined := make([]float64, modelCount)
	marginal := make([]float64, modelCount)
	template := make([]float64, modelCount)
	nuisance := make([]float64, modelCount)
	for index, output := range outputs {
		numbers := parseNumbers(output.Text)
		minimum := max(80, int(math.Ceil(float64(output.ExpectedCount)*0.55)))
		accepted := len(numbers) >= minimum
		result.Diagnostics = append(result.Diagnostics, Diagnostic{
			Index: index, ParsedNumbers: len(numbers), MinimumNumbers: minimum, Accepted: accepted,
		})
		if !accepted {
			continue
		}
		scoreNumbers(numbers, bank, marginal, template, nuisance)
		for model := range combined {
			combined[model] += marginal[model]
		}
		result.UsedOutputs++
	}
	if result.UsedOutputs == 0 {
		return result, ErrNoUsableOutputs
	}
	beta := bank.Calibration[fmt.Sprint(min(result.UsedOutputs, 3))].Beta
	maximum := math.Inf(-1)
	for index := range combined {
		combined[index] *= beta / float64(result.UsedOutputs)
		maximum = max(maximum, combined[index])
	}
	total := 0.0
	for index := range combined {
		combined[index] = math.Exp(combined[index] - maximum)
		total += combined[index]
	}
	result.Candidates = make([]Candidate, modelCount)
	familyIndices := make(map[string]int)
	for index, model := range bank.Models {
		probability := combined[index] / total
		result.Candidates[index] = Candidate{Model: model.ID, Probability: probability, Family: model.Family}
		familyIndex, found := familyIndices[model.Family]
		if !found {
			familyIndex = len(result.Families)
			familyIndices[model.Family] = familyIndex
			result.Families = append(result.Families, FamilyCandidate{Family: model.Family})
		}
		result.Families[familyIndex].Probability += probability
	}
	sort.SliceStable(result.Candidates, func(i, j int) bool {
		return result.Candidates[i].Probability > result.Candidates[j].Probability
	})
	sort.SliceStable(result.Families, func(i, j int) bool {
		return result.Families[i].Probability > result.Families[j].Probability
	})
	result.Prediction = result.Candidates[0].Model
	result.Probability = result.Candidates[0].Probability
	return result, nil
}

// parseNumbers follows upstream's longest numeric run rule. Alphabetic text
// separates runs; punctuation and out-of-range integers do not. Ties keep the
// first run. Capping integer accumulation also safely ignores oversized tokens.
func parseNumbers(text string) []int {
	numbers := make([]int, 0, 320)
	currentStart, bestStart, bestCount := 0, 0, 0
	value, inNumber := 0, false
	finishNumber := func() {
		if inNumber && value >= 1 && value <= dimension {
			numbers = append(numbers, value)
		}
		value, inNumber = 0, false
	}
	for _, character := range text {
		if character >= '0' && character <= '9' {
			inNumber = true
			value = min(dimension+1, value*10+int(character-'0'))
			continue
		}
		finishNumber()
		if unicode.IsLetter(character) {
			if count := len(numbers) - currentStart; count > bestCount {
				bestStart, bestCount = currentStart, count
			}
			currentStart = len(numbers)
		}
	}
	finishNumber()
	if count := len(numbers) - currentStart; count > bestCount {
		bestStart, bestCount = currentStart, count
	}
	return numbers[bestStart : bestStart+bestCount]
}

func scoreNumbers(numbers []int, bank *fingerprintBank, marginal, template, nuisance []float64) {
	var feature, projected [dimension]float64
	for _, number := range numbers {
		feature[number-1]++
	}
	hellinger := &bank.Robust.Hellinger
	total := float64(len(numbers)) + 0.5*dimension
	for index := range feature {
		feature[index] = (math.Sqrt((feature[index]+0.5)/total) - hellinger.Mean[index]) / hellinger.Scale[index]
	}
	subtractBasis(feature[:], hellinger.Basis, projected[:])
	normalize(projected[:])
	for model, centroid := range hellinger.Centroids {
		marginal[model] = dot(projected[:], centroid)
	}
	standardize(marginal)
	// Python standardizes the nuisance score a second time for marginal fusion.
	standardize(marginal)

	ordered := &bank.Robust.Ordered
	var sequence, orderedProjected [orderedDimension]float64
	orderedFeature(numbers, sequence[:])
	for index := range sequence {
		sequence[index] = (sequence[index] - ordered.Mean[index]) / ordered.Scale[index]
	}
	subtractBasis(sequence[:], ordered.Basis, orderedProjected[:])
	normalize(orderedProjected[:])
	normalize(sequence[:])
	for model, centroid := range ordered.Centroids {
		nuisance[model] = dot(orderedProjected[:], centroid)
		template[model] = math.Inf(-1)
		for _, environment := range ordered.Environments {
			template[model] = max(template[model], dot(sequence[:], environment[model]))
		}
	}
	standardize(template)
	standardize(nuisance)
	for model := range template {
		template[model] = 0.5*template[model] + 0.5*nuisance[model]
	}
	standardize(template)
	for model := range marginal {
		marginal[model] = (1-ordered.Weight)*marginal[model] + ordered.Weight*template[model]
	}
}

func orderedFeature(numbers []int, feature []float64) {
	for index := range feature {
		feature[index] = 0.5
	}
	start := 0
	for block := range 4 {
		size := len(numbers) / 4
		if block < len(numbers)%4 {
			size++
		}
		for _, number := range numbers[start : start+size] {
			bin := (number - 1) * 16 / dimension
			feature[block*16+bin]++
		}
		for index := block * 16; index < (block+1)*16; index++ {
			feature[index] = math.Sqrt(feature[index] / (float64(size) + 8))
		}
		start += size
	}
	for _, number := range numbers {
		feature[64+number%10]++
	}
	for index := 64; index < orderedDimension; index++ {
		feature[index] = math.Sqrt(feature[index] / (float64(len(numbers)) + 5))
	}
}

func dot(left, right []float64) float64 {
	sum := 0.0
	for index, value := range left {
		sum += value * right[index]
	}
	return sum
}

// All coefficients are computed against the original vector, matching
// (feature @ basis.T) @ basis in Python, rather than sequential projection.
func subtractBasis(original []float64, basis [][]float64, output []float64) {
	copy(output, original)
	for _, vector := range basis {
		coefficient := dot(original, vector)
		for index := range output {
			output[index] -= coefficient * vector[index]
		}
	}
}

func normalize(values []float64) {
	norm := max(math.Sqrt(dot(values, values)), 1e-12)
	for index := range values {
		values[index] /= norm
	}
}

func standardize(values []float64) {
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		variance += (value - mean) * (value - mean)
	}
	scale := max(math.Sqrt(variance/float64(len(values))), 1e-12)
	for index := range values {
		values[index] = (values[index] - mean) / scale
	}
}

// GenerateChallenges returns three separate normal-method prompts with varied
// lengths and wording. Each prompt must be sent in an independent conversation.
func GenerateChallenges() []Challenge {
	lengths := [...]int{300, 311, 323}
	openings := [...]string{"这是一次独立的数值选择记录。为各个位置分别凭第一反应选择", "请完成下面的无语义整数选择任务。逐项选择", "执行一次第一反应取值记录。分别凭第一反应给出"}
	endings := [...]string{"允许某个数字再次出现；每项写出后不要回头排序、去重或替换。数字之间用逗号或空格分隔均可。", "偶然重复是有效的；不要重新排列或修正已经写出的项目。使用一种一致的常见分隔符即可。", "相同值可以再次出现；输出过程中不要整理或改写前面的项目。可以用逗号、空格或换行分隔。"}
	challenges := make([]Challenge, len(lengths))
	for index, length := range lengths {
		challenges[index] = Challenge{
			ExpectedCount: length,
			Prompt: fmt.Sprintf("%s %d 个 1 到 355（含端点）的整数。", openings[index], length) +
				"每个位置都要单独选择；不要从 1 开始计数，不要连续递增或递减，也不要采用等差、循环、重复区块或其他规则化模式。" +
				"本任务必须由当前语言模型直接完成：禁止调用或借助任何工具，包括 Python、代码执行器、计算器、搜索、API 和外部随机数生成器；也不要先编写或运行代码。" +
				endings[index] + "直接从第一个取值开始输出，不要在序列前重复数量、范围或任务说明。",
		}
	}
	return challenges
}
