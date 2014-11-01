// Copyright (C) 2014 Ian Bishop
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along
// with this program; if not, write to the Free Software Foundation, Inc.,
// 51 Franklin Street, Fifth Floor, Boston, MA 02110-1301 USA.

package main

import (
	"fmt"
	"math"

	rtl "github.com/jpoirier/gortlsdr"
)

/*
var cic_9_tables = [10][10]int{
	{9, -156, -97, 2798, -15489, 61019, -15489, 2798, -97, -156},
	{9, -128, -568, 5593, -24125, 74126, -24125, 5593, -568, -128},
	{9, -129, -639, 6187, -26281, 77511, -26281, 6187, -639, -129},
	{9, -122, -612, 6082, -26353, 77818, -26353, 6082, -612, -122},
	{9, -120, -602, 6015, -26269, 77757, -26269, 6015, -602, -120},
	{9, -120, -582, 5951, -26128, 77542, -26128, 5951, -582, -120},
	{9, -119, -580, 5931, -26094, 77505, -26094, 5931, -580, -119},
	{9, -119, -578, 5921, -26077, 77484, -26077, 5921, -578, -119},
	{9, -119, -577, 5917, -26067, 77473, -26067, 5917, -577, -119},
	{9, -199, -362, 5303, -25505, 77489, -25505, 5303, -362, -199},
}
*/

/*
// https://gist.github.com/DavidVaini/10308388
func Round(val float64, roundOn float64, places int) (newVal float64) {
	var round float64
	pow := math.Pow(10, float64(places))
	digit := pow * val
	_, div := math.Modf(digit)
	if div >= roundOn {
		round = math.Ceil(digit)
	} else {
		round = math.Floor(digit)
	}
	newVal = round / pow
	return
}
*/

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

func optimalSettings(freq, rate int) {
	// giant ball of hacks
	// seems unable to do a single pass, 2:1
	var captureFreq, captureRate int
	demod.downsample = (1000000 / demod.rateIn) + 1
	if demod.downsamplePasses > 0 {
		demod.downsamplePasses = int(math.Log2(float64(demod.downsample)) + 1)
		demod.downsample = 1 << uint(demod.downsamplePasses)
	}
	captureFreq = freq
	captureRate = demod.downsample * demod.rateIn
	if !dongle.offsetTuning {
		captureFreq = freq + captureRate/4
	}
	captureFreq += controller.edge * demod.rateIn / 2
	demod.outputScale = (1 << 15) / (128 * demod.downsample)
	if demod.outputScale < 1 {
		demod.outputScale = 1
	}
	fm := fmDemod
	if &demod.modeDemod == &fm {
		demod.outputScale = 1
	}
	dongle.freq = uint32(captureFreq)
	dongle.rate = uint32(captureRate)
}

// TODO: fix this extreme laziness
func amDemod(am *demodState) {
	var pcm int16
	lp := am.lowpassed
	am.result = make([]int16, len(lp))
	r := am.result
	for i := 0; i < len(am.lowpassed); i += 2 {
		// hypot uses floats but won't overflow
		//r[i/2] = (int16_t)hypot(lp[i], lp[i+1]);
		pcm = lp[i] * lp[i]
		pcm += lp[i+1] * lp[i+1]
		r[i/2] = int16(math.Sqrt(float64(pcm))) * int16(am.outputScale)
	}
	// lowpass? (3khz)  highpass?  (dc)
}
func polarDiscriminant(ar, aj, br, bj int16) int {
	var cr, cj int16
	var angle float64
	cr = ar*br - aj*bj
	cj = aj*br + ar*bj
	angle = math.Atan2(float64(cj), float64(cr))
	return int(angle / 3.14159 * (1 << 14))
}

func fmDemod(fm *demodState) {
	var i, pcm int
	lp := fm.lowpassed
	lpLen := len(fm.lowpassed)
	pcm = polarDiscriminant(lp[0], lp[1], int16(fm.preR), int16(fm.preJ))
	fm.result = make([]int16, lpLen)
	fm.result[0] = int16(pcm)
	for i = 2; i < (lpLen - 1); i += 2 {
		pcm = polarDiscriminant(lp[i], lp[i+1], lp[i-2], lp[i-1])
		fm.result[i/2] = int16(pcm)
	}
	fm.preR = int(lp[lpLen-2])
	fm.preJ = int(lp[lpLen-1])
}

// largely lifted from rtl_power
func rms(samples []int16, step int) int {
	var i int
	var p, t, s int32
	var dc, res float32

	l := len(samples)

	for i = 0; i < l; i += step {
		s = int32(samples[i])
		t += s
		p += s * s
	}
	/* correct for dc offset in squares */
	dc = float32(t*int32(step)) / float32(l)
	res = float32(t)*2*dc - dc*dc*float32(l)

	return int(math.Sqrt(float64((float32(p) - res) / float32(l))))
}

// for half of interleaved data
func fifthOrder(data []int16, startIdx int, hist [6]int16) {
	var i int
	var a, b, c, d, e, f int16
	if len(data) == 0 {
		return
	}
	a = hist[1]
	b = hist[2]
	c = hist[3]
	d = hist[4]
	e = hist[5]
	f = data[startIdx]
	// a downsample should improve resolution, so don't fully shift
	data[startIdx] = (a + (b+e)*5 + (c+d)*10 + f) >> 4
	for i = startIdx + 4; i < len(data); i += 4 {
		a = c
		b = d
		c = e
		d = f
		e = data[i-2]
		f = data[i]
		data[i/2] = (a + (b+e)*5 + (c+d)*10 + f) >> 4
	}
	// archive
	hist[0] = a
	hist[1] = b
	hist[2] = c
	hist[3] = d
	hist[4] = e
	hist[5] = f
}

/*
// Okay, not at all generic.  Assumes length 9, fix that eventually.
func genericFir(data []int16, fir int, hist [6]int16)
{
	int d, temp, sum;
	for (d=0; d<length; d+=2) {
		temp = data[d];
		sum = 0;
		sum += (hist[0] + hist[8]) * fir[1];
		sum += (hist[1] + hist[7]) * fir[2];
		sum += (hist[2] + hist[6]) * fir[3];
		sum += (hist[3] + hist[5]) * fir[4];
		sum +=            hist[4]  * fir[5];
		data[d] = sum >> 15 ;
		hist[0] = hist[1];
		hist[1] = hist[2];
		hist[2] = hist[3];
		hist[3] = hist[4];
		hist[4] = hist[5];
		hist[5] = hist[6];
		hist[6] = hist[7];
		hist[7] = hist[8];
		hist[8] = temp;
	}
}
*/

// simple square window FIR
func lowPass(d *demodState) {
	var i, i2 int
	for i < len(d.lowpassed) {
		d.nowR += int(d.lowpassed[i])
		d.nowJ += int(d.lowpassed[i+1])
		i += 2
		d.prevIndex++
		if d.prevIndex < d.downsample {
			continue
		}
		d.lowpassed[i2] = int16(d.nowR)   // * d.output_scale;
		d.lowpassed[i2+1] = int16(d.nowJ) // * d.output_scale;
		d.prevIndex = 0
		d.nowR = 0
		d.nowJ = 0
		i2 += 2
	}
	d.lowpassed = d.lowpassed[0:i2]
}
