package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	// "unsafe"

	rtl "github.com/jpoirier/gortlsdr"
)

const (
	DEFAULT_SAMPLE_RATE = 24000
	DEFAULT_BUF_LENGTH  = (1 * 16384)
	MAXIMUM_OVERSAMPLE  = 16
	MAXIMUM_BUF_LENGTH  = (MAXIMUM_OVERSAMPLE * DEFAULT_BUF_LENGTH)
	AUTO_GAIN           = -100
	BUFFER_DUMP         = 4096

	CIC_TABLE_MAX = 10

	FREQUENCIES_LIMIT = 1000
)

type frequencies []uint32

type dongleState struct {
	exitFlag       int
	dev            *rtl.Context
	devIndex       int
	freq           uint32
	rate           uint32
	gain           int
	ppmError       int
	offsetTuning   int
	directSampling int
	mute           int
	demodTarget    *demodState
}

type demodState struct {
	exitFlag       int
	lowpassed      []int16
	lpIHist        [10][6]int16
	lpQHist        [10][6]int16
	result         [MAXIMUM_BUF_LENGTH]int16 // ?
	resultLen      int                       // ?
	demodDataChan  chan []int16
	deviceDataChan chan []uint16
	droopIHist     [9]int16
	droopQHist     [9]int16
	rateIn         int
	rateOut        int
	//rateOut2           int
	nowR               int
	nowJ               int
	preR               int
	preJ               int
	prevIndex          int
	downsample         int // min 1, max 256
	postDownsample     int
	outputScale        int
	squelchLevel       int
	conseqSquelch      int
	squelchHits        int
	terminateOnSquelch int
	downsamplePasses   int
	compFirSize        int
	customAtan         int
	deemph, deemphA    int
	nowLpr             int
	prevLprIndex       int
	dcBlock, dcAvg     int
	modeDemod          func(fm *demodState)
	outputTarget       *outputState
}

type outputState struct {
	exitFlag      int
	file          *os.File
	filename      string
	demodDataChan chan []int16
	rate          int
}

type controllerState struct {
	exitFlag int
	freqs    frequencies
	freqNow  int
	edge     int
	wbMode   int
}

var dongle *dongleState
var demod *demodState
var output *outputState
var controller *controllerState

var ACTUAL_BUF_LENGTH int
var lcmPost = [17]int{1, 1, 1, 3, 1, 5, 3, 7, 1, 9, 5, 11, 3, 13, 7, 15, 1}

func init() {

	dongle = &dongleState{}
	demod = &demodState{}
	output = &outputState{}
	controller = &controllerState{}

	dongle.Init()
	demod.Init()
	output.Init()
	controller.Init()
}

func (s *dongleState) Init() {
	s.rate = DEFAULT_SAMPLE_RATE
	s.gain = AUTO_GAIN // tenths of a dB
	s.demodTarget = demod
}

func (s *demodState) Init() {
	s.rateIn = DEFAULT_SAMPLE_RATE
	s.rateOut = DEFAULT_SAMPLE_RATE
	s.conseqSquelch = 10
	s.squelchHits = 11
	s.postDownsample = 1 // once this works, default = 4
	s.modeDemod = fmDemod
	s.outputTarget = output
}

func (s *controllerState) Init() {
	s.freqs = append(s.freqs, 100000000)
}

func (s *outputState) Init() {
	s.rate = DEFAULT_SAMPLE_RATE
}

func fmDemod(fm *demodState) {
	var i, pcm int
	lp := fm.lowpassed
	lpLen := len(fm.lowpassed)
	pcm = polarDiscriminant(lp[0], lp[1], int16(fm.preR), int16(fm.preJ))
	fm.result[0] = int16(pcm)
	for i = 2; i < (lpLen - 1); i += 2 {
		pcm = polarDiscriminant(lp[i], lp[i+1], lp[i-2], lp[i-1])
		fm.result[i/2] = int16(pcm)
	}
	fm.preR = int(lp[lpLen-2])
	fm.preJ = int(lp[lpLen-1])
	fm.resultLen = lpLen / 2
}

func polarDiscriminant(ar, aj, br, bj int16) int {
	var cr, cj int16
	var angle float64
	cr = ar*br - aj*bj
	cj = aj*br + ar*bj
	angle = math.Atan2(float64(cj), float64(cr))
	return int(angle / 3.14159 * (1 << 14))
}

func (f frequencies) String() string {
	return fmt.Sprintf("%d", f)
}

func (f *frequencies) Set(val string) (err error) {

	var i int
	var start, stop, step int

	step = 25000

	bits := strings.Split(val, ":")

	fmt.Printf("bits len %d", len(bits))

	switch len(bits) {
	case 1:
		i, err = strconv.Atoi(bits[0])
		if err != nil {
			return
		}
		freqs := *f
		freqs = append(freqs, uint32(i))
		*f = freqs
		return
	case 3:
		step, err = strconv.Atoi(bits[2])
		if err != nil {
			return
		}
		fallthrough
	case 2:
		start, err = strconv.Atoi(bits[0])
		if err != nil {
			return
		}
		stop, err = strconv.Atoi(bits[1])
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("Frequency range could not be parsed")
		return
	}

	for j := start; j <= stop; j += step {
		freqs := *f
		if len(freqs) > FREQUENCIES_LIMIT {
			break
		}
		freqs = append(freqs, uint32(j))
		*f = freqs
	}
	return
}

//func (dev *rtl.Context) nearestGain(targetGain int) (err error, nearest int) {
func nearestGain(dev *rtl.Context, targetGain int) (nearest int, err error) {
	err = dev.SetTunerGainMode(true)
	if err != nil {
		return
	}
	gains, err := dev.GetTunerGains()
	if err != nil {
		return
	}

	if len(gains) == 0 {
		err = fmt.Errorf("No gains returned")
		return
	}
	nearest = gains[0]
	for i := 0; i < len(gains); i++ {
		res1 := math.Abs(float64(targetGain - nearest))
		res2 := math.Abs(float64(targetGain - gains[i]))
		if res2 < res1 {
			nearest = gains[i]
		}
	}
	return
}

// TODO: fix this extreme laziness
func amDemod(am *demodState) {
	var pcm int16
	lp := am.lowpassed
	r := am.result
	for i := 0; i < len(am.lowpassed); i += 2 {
		// hypot uses floats but won't overflow
		//r[i/2] = (int16_t)hypot(lp[i], lp[i+1]);
		pcm = lp[i] * lp[i]
		pcm += lp[i+1] * lp[i+1]
		r[i/2] = int16(math.Sqrt(float64(pcm))) * int16(am.outputScale)
	}
	am.resultLen = len(am.lowpassed) / 2
	// lowpass? (3khz)  highpass?  (dc)
}

//static void rtlsdr_callback(unsigned char *buf, uint32_t len, void *ctx)
func rtlsdrCallback(buf []byte, ctx *rtl.UserCtx) {

	var i int
	var s *dongleState
	var d *demodState

	if s, ok := (*ctx).(*dongleState); ok {
		d = s.demodTarget
	} else {
		return
	}

	if s.mute > 0 && s.mute < len(buf) {
		for i = 0; i < s.mute; i++ {
			buf[i] = 127
		}
		s.mute = 0
	}
	if s.offsetTuning == 0 {
		//rotate_90(buf, len)
	}
	var buf16 []uint16
	for i = 0; i < len(buf); i++ {
		buf16 = append(buf16, uint16(buf[i]-127))
	}
	d.deviceDataChan <- buf16
	//d.lowpassed = append(d.lowpassed, s.buf16)
	//memcpy(d->lowpassed, s->buf16, 2*len);
}

func dongleRoutine(s *dongleState) {
	var ctx rtl.UserCtx = s
	err := dongle.dev.ReadAsync(rtlsdrCallback, &ctx, rtl.DefaultAsyncBufNumber, rtl.DefaultBufLength)
	if err != nil {
		log.Printf("\tReadAsync Fail - error: %s\n", err)
	}
}

func demodRoutine(d *demodState) {
	//o := d.outputTarget
	for {
		d.fullDemod()

		// check exit?

		if d.squelchLevel > 0 && d.squelchHits > d.conseqSquelch {
			// hair trigger
			d.squelchHits = d.conseqSquelch + 1
			continue
		}
		//memcpy(o->result, d->result, 2*d->result_len);
		//o.demodDataChan <- d.demodDataChan
	}
}

func (d *demodState) fullDemod() {
	var i, dsP int
	//var sr int
	lpLen := len(d.lowpassed)
	dsP = d.downsamplePasses
	if dsP > 0 {
		for i = 0; i < dsP; i++ {
			fifthOrder(d.lowpassed, 0, d.lpIHist[i])
			fifthOrder(d.lowpassed, 1, d.lpQHist[i])
		}
		lpLen = lpLen >> uint(dsP)
		/* droop compensation */
		if d.compFirSize == 9 && dsP <= CIC_TABLE_MAX {
			//generic_fir(d->lowpassed, d->lp_len, cic_9_tables[ds_p], d->droop_i_hist);
			//generic_fir(d->lowpassed+1, d->lp_len-1, cic_9_tables[ds_p], d->droop_q_hist);
		}
	} else {
		//low_pass(d);
	}

	/*
		// power squelch
		if d.squelch_level > 0 {
			sr = rms(d.lowpassed, 1);
			if (sr < d->squelch_level) {
				d->squelch_hits++;
				for (i=0; i<d->lp_len; i++) {
					d->lowpassed[i] = 0;
				}
			} else {
				d->squelch_hits = 0;}
		}
		// lowpassed -> result
		d->mode_demod(d);
		if (d->mode_demod == &raw_demod) {
			return;
		}
		// TODO: fm noise squelch
		// use nicer filter here too?
		if (d->post_downsample > 1) {
			d->result_len = low_pass_simple(d->result, d->result_len, d->post_downsample);}
		if (d->deemph) {
			deemph_filter(d);}
		if (d->dc_block) {
			dc_block_filter(d);}
		if (d->rate_out2 > 0) {
			low_pass_real(d);
			//arbitrary_resample(d->result, d->result, d->result_len, d->result_len * d->rate_out2 / d->rate_out);
		}
	*/
}

func main() {

	var err error

	flag.IntVar(&dongle.devIndex, "d", 0, "dongle device index")
	flag.Var(&controller.freqs, "f", "frequency or range of frequencies, and step e.g 92900:100100:25000")
	flag.IntVar(&demod.squelchLevel, "l", 0, "squelch level")
	flag.IntVar(&demod.rateIn, "s", 0, "sample rate")
	flag.IntVar(&dongle.ppmError, "p", 0, "ppm error")
	demodMode := flag.String("M", "am", "demodulation mode [fm, am]")

	flag.Parse()

	switch *demodMode {
	case "fm":
		demod.modeDemod = fmDemod
	case "am":
		demod.modeDemod = amDemod
	}

	demod.rateOut = demod.rateIn

	if len(controller.freqs) == 0 {
		log.Fatalln("Please specify a frequency.")
	}

	if len(controller.freqs) >= FREQUENCIES_LIMIT {
		log.Fatalf("Too many channels, maximum %d.\n", FREQUENCIES_LIMIT)
	}

	if len(controller.freqs) > 1 && demod.squelchLevel == 0 {
		log.Fatalln("Please specify a squelch level.  Required for scanning multiple frequencies.")
	}

	/* quadruple sample_rate to limit to Δθ to ±π/2 */
	demod.rateIn *= demod.postDownsample

	if output.rate == 0 {
		output.rate = demod.rateOut
	}

	if len(controller.freqs) > 1 {
		demod.terminateOnSquelch = 0
	}

	if flag.Arg(0) != "" {
		output.filename = flag.Arg(0)
	} else {
		output.filename = "-"
	}

	ACTUAL_BUF_LENGTH = lcmPost[demod.postDownsample] * DEFAULT_BUF_LENGTH

	dongle.dev, err = rtl.Open(dongle.devIndex)
	if err != nil {
		log.Fatalf("Failed to open dongle, '%s', exiting\n", err)
	}
	defer dongle.dev.Close()

	/* Set the tuner gain */
	if dongle.gain == AUTO_GAIN {
		err = dongle.dev.SetTunerGainMode(false)
		if err != nil {
			log.Fatalf("Error setting tuner auto-gain: %s", err)
		}
	} else {
		dongle.gain, err = nearestGain(dongle.dev, dongle.gain)
		if err != nil {
			log.Fatalf("Error getting nearest gain to %d: %s", dongle.gain, err)
		}
		err = dongle.dev.SetTunerGain(dongle.gain)
		if err != nil {
			log.Fatalf("Error setting tuner manual gain to %d: %s", dongle.gain)
		}
	}

	if dongle.ppmError > 0 {
		err = dongle.dev.SetFreqCorrection(dongle.ppmError)
		if err != nil {
			log.Fatalf("Error setting frequency correction to %d: %s", dongle.ppmError, err)
		}
	}

	if output.filename == "-" {
		output.file = os.Stdout
	} else {
		output.file, err = os.OpenFile(output.filename, os.O_RDWR|os.O_APPEND, 0660)
		if err != nil {
			log.Fatalln(err)
		}
	}
	defer output.file.Close()

	/* Reset endpoint before we start reading from it (mandatory) */
	err = dongle.dev.ResetBuffer()
	if err != nil {
		log.Fatalln(err)
	}

	log.Printf("Exiting...\n")
}
