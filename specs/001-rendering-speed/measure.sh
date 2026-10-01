#!/usr/bin/env bash
# Session S1 of specs/001-rendering-speed: drives a measuring qws instance with
# xdotool and summarises its frame timings and profiles. With HOLD=2 it is
# session S2 of specs/007-animation: each activation ends with a step key
# held for 2 s, as the author's autorepeat gives it — a press, then
# REPEAT_RATE presses a second after REPEAT_DELAY ms — and then, instead of
# Escape, with steps back to the first window, the one focused, and the
# modifier released: an activation that leaves the focus where it was.
# With SWEEP=1 as well it is session S3 of specs/010-animation-options: before
# the held key the pointer sweeps across the middle of the overlay, 40 moves
# 60 ms apart, diagonally — from 10 % to 90 % of its width and from 42 % to
# 58 % of its height, so that it crosses the tiles of the grid rather than the
# gap between two rows — and is then put on the centre of the focused window,
# so that focus, which follows the mouse on ws1, stays where it was.
#
#   measure.sh run <renderer> <outdir>   one run of S1, renderer cpu or glx
#   measure.sh summary <outdir>          summary of a finished run
#   measure.sh k3 <outdir>               criterion K3 for cpu and glx
#
# The instance is the ./qws binary of the repository (make build), or $QWS,
# with the user's configuration, debug logging in JSON, profiling, and the key
# combination Alt+F11, which neither i3 nor the running instances grab on
# ws1. Before the session it focuses every i3 window, least recent first, so
# that it has their thumbnails; focus ends where it was. Every key is sent
# after the frame of the previous one is logged, so that events of one
# activation never queue behind another. Hands off the keyboard and mouse
# during a run.
set -euo pipefail

ACTIVATIONS=${ACTIVATIONS:-20}
STEPS=${STEPS:-23}
KEY=${KEY:-F11}
LAYOUT=${LAYOUT:-}
HOLD=${HOLD:-0}
SWEEP=${SWEEP:-0}
REPEAT_DELAY=${REPEAT_DELAY:-500}
REPEAT_RATE=${REPEAT_RATE:-33}

# Presses of the held key: the first, and the repeats after the delay
held=0
if ((HOLD > 0)); then
	held=$((1 + (HOLD * 1000 - REPEAT_DELAY) * REPEAT_RATE / 1000))
fi

root=$(git -C "$(dirname "$0")" rev-parse --show-toplevel)
qws=${QWS:-$root/qws}
render_fn='github.com/almaz-uno/qws/pkg/ui.(*Selector).render'

# i3 windows in focus order, most recent first, without bars and scratchpad
focus_order() {
	i3-msg -t get_tree | jq -r '
		def walk_focus:
			if .window != null then .window
			else (.nodes + .floating_nodes) as $kids
				| .focus[] as $id | $kids[] | select(.id == $id)
				| select(.type != "dockarea" and .name != "__i3_scratch")
				| walk_focus
			end;
		walk_focus'
}

# Number of frames logged so far
frame_count() {
	grep -c '"message":"Frame"' "$1" || true
}

# Waits until the log has at least $2 frames, for 5 s at most
wait_frames() {
	local i
	for ((i = 0; i < 500; i++)); do
		(($(frame_count "$1") >= $2)) && return 0
		sleep 0.01
	done
	echo "no frame $2 in 5 s" >&2
	return 1
}

# Waits until activation $3 is ready for keys, for 5 s at most: the log has
# $2 frames — the first, and the frame of the Expose that mapping causes — or,
# where the overlay fades in, the fade-in of the activation is at rest
wait_ready() {
	local i
	for ((i = 0; i < 500; i++)); do
		(($(frame_count "$1") >= $2)) && return 0
		grep -qE '"kind":"fade-in","animation":[0-9]+,"activation":'"$3"',.*"at_rest":true' "$1" && return 0
		sleep 0.01
	done
	echo "activation $3 not ready in 5 s" >&2
	return 1
}

# Centre of the focused i3 window, "x y"
focused_centre() {
	i3-msg -t get_tree | jq -r '
		.. | objects | select(.focused == true) | .rect
		| "\(.x + (.width / 2 | floor)) \(.y + (.height / 2 | floor))"'
}

# Sweeps the pointer diagonally across the middle of the overlay, as the log of
# $1 last placed it, and puts it on the centre of the focused window
sweep() {
	local x y w h fx fy i
	read -r x y w h < <(jq -rs 'map(select(.message == "Monitor changed, recreating selector window"))
		| last | "\(.x) \(.y) \(.width) \(.height)"' "$1")
	read -r fx fy < <(focused_centre)
	for ((i = 0; i < 40; i++)); do
		xdotool mousemove $((x + w / 10 + i * (w * 8 / 10) / 39)) $((y + h * 42 / 100 + i * (h * 16 / 100) / 39))
		sleep 0.06
	done
	xdotool mousemove "$fx" "$fy"
}

warm_up() {
	local w
	for w in $(focus_order | tac); do
		i3-msg -q "[id=$w] focus" || true
		sleep 0.35
	done
}

run() {
	local renderer=$1 out=$2 log=$2/log.json pid a s n
	[[ -x $qws ]] || { echo "no $qws, run make build" >&2; exit 1; }
	mkdir -p "$out"
	{
		echo "date: $(date -Iseconds)"
		echo "commit: $(git -C "$root" rev-parse --short HEAD)$(git -C "$root" diff --quiet HEAD || echo ' (dirty)')"
		echo "binary: $qws"
		echo "renderer: $renderer"
		echo "layout: ${LAYOUT:-configured}"
		echo "i3 windows: $(focus_order | wc -l)"
		echo "activations: $ACTIVATIONS, steps: $STEPS"
		echo "held: ${HOLD}s, $held keys"
		echo "sweep: $SWEEP"
		echo "keys per activation: $((STEPS + held))"
	} >"$out/meta"
	cp "$qws" "$out/qws"

	# Both names of the variable: the dotted one is what Viper reads without a
	# key replacer, the other what it reads with one (002-config-names)
	env 'QWS_LOG.FORMAT=json' QWS_LOG_FORMAT=json "$out/qws" -v -r "$renderer" -m Alt -k "$KEY" \
		--behavior-show-delay 0 ${LAYOUT:+--appearance-layout "$LAYOUT"} \
		--cpuprofile "$out/cpu.prof" --memprofile "$out/mem.prof" \
		2>"$log" &
	pid=$!
	trap 'xdotool keyup alt; kill -INT '"$pid"' 2>/dev/null || true' EXIT
	sleep 1
	kill -0 "$pid"

	warm_up

	for ((a = 1; a <= ACTIVATIONS; a++)); do
		n=$(frame_count "$log")
		xdotool keydown alt key "$KEY"
		wait_ready "$log" $((n + 2)) "$a" || true
		for ((s = 1; s <= STEPS; s++)); do
			n=$(frame_count "$log")
			xdotool key Right
			wait_frames "$log" $((n + 1)) || true
		done
		if ((SWEEP > 0)); then
			sweep "$log"
		fi
		if ((held > 0)); then
			xdotool key Right
			sleep "$(awk -v d="$REPEAT_DELAY" 'BEGIN { print d / 1000 }')"
			if ((held > 1)); then
				# shellcheck disable=SC2046
				xdotool key --delay $((1000 / REPEAT_RATE)) $(yes Right | head -n $((held - 1)))
			fi
			# The step of the last key ends on the window the keys lead to from
			# the first frame (K5 of specs/007-animation): in 150 ms, but its
			# frame at rest comes when drawn — some 500 ms for the grid. Only
			# on that window does the run step back and release the modifier;
			# else Escape, so that no other window is activated.
			want=-1
			for ((i = 0; i < 500; i++)); do
				read -r first sel n < <(jq -rs --argjson a "$a" '
					map(select(.message == "Frame" and .activation == $a))
					| "\(first | .selected) \(last | .selected) \(last | .windows)"' "$log")
				want=$(((first + STEPS + held) % n))
				((sel == want)) && break
				sleep 0.01
			done
			echo "activation $a: at $sel, want $want" >>"$out/k5"
			if ((sel == want)); then
				back=$(((n - sel) % n))
				if ((back > 0)); then
					# shellcheck disable=SC2046
					xdotool key --delay 200 $(yes Right | head -n "$back")
				fi
				sleep 0.4
				xdotool keyup alt
			else
				xdotool key Escape keyup alt
			fi
		else
			xdotool key Escape keyup alt
		fi
		sleep 0.3
		kill -0 "$pid"
	done

	kill -INT "$pid"
	wait "$pid" || true
	trap - EXIT
	summary "$out" | tee "$out/summary"
}

# Criterion K3: for three activations of each renderer, the contents of the
# overlay window after the first frame equal the frame the instance dumped
k3() {
	local out=$1 renderer log pid a n file win w h rc=0
	mkdir -p "$out"
	go build -o "$out/framecmp" "$root/console/framecmp"
	for renderer in cpu glx; do
		mkdir -p "$out/$renderer"
		log=$out/$renderer/log.json
		env 'QWS_LOG.FORMAT=json' QWS_LOG_FORMAT=json "$qws" -v -r "$renderer" -m Alt -k "$KEY" \
			--debug-dump-frames "$out/$renderer" 2>"$log" &
		pid=$!
		trap 'xdotool keyup alt; kill -INT '"$pid"' 2>/dev/null || true' EXIT
		sleep 1
		kill -0 "$pid"

		for ((a = 1; a <= 3; a++)); do
			n=$(frame_count "$log")
			xdotool keydown alt key "$KEY"
			wait_ready "$log" $((n + 2)) "$a" || true
			read -r file win w h < <(jq -r --argjson a "$a" \
				'select(.message == "Frame dumped" and .activation == $a) | "\(.file) \(.window) \(.width) \(.height)"' "$log")
			printf '%s, activation %d: ' "$renderer" "$a"
			"$out/framecmp" "$file" "$w" "$h" "$win" || rc=1
			xdotool key Escape keyup alt
			sleep 0.3
		done

		kill -INT "$pid"
		wait "$pid" || true
		trap - EXIT
		if grep -q 'falling back to CPU' "$log"; then
			echo "WARNING: $renderer fell back to cpu"
			rc=1
		fi
	done
	return $rc
}

# Nearest-rank p50 and p95 of the numbers on stdin
pct() {
	sort -g | awk '{ v[NR] = $1 }
		END {
			if (NR == 0) { print "n=0"; exit }
			printf "n=%d p50=%.2f p95=%.2f max=%.2f\n", NR,
				v[int((NR * 50 + 99) / 100)], v[int((NR * 95 + 99) / 100)], v[NR]
		}'
}

summary() {
	local out=$1 f n cpu alloc
	frames() { jq -r "select(.message == \"Frame\") | $1" "$out/log.json"; }

	cat "$out/meta"
	if grep -q 'falling back to CPU' "$out/log.json"; then
		echo "WARNING: glx fell back to cpu"
	fi
	echo "qws windows: $(frames .windows | sort -u | tr '\n' ' ')"
	echo "frames by cause: $(frames .cause | sort | uniq -c | awk '{ printf "%s %s  ", $2, $1 }')"
	for f in total_ms list_ms show_ms draw_ms present_ms; do
		printf 'M1 cold %-10s ' "$f"
		frames "select(.cause == \"activation\" and .cold) | .$f" | pct
	done
	for f in total_ms list_ms show_ms draw_ms present_ms; do
		printf 'M1 warm %-10s ' "$f"
		frames "select(.cause == \"activation\" and (.cold | not)) | .$f" | pct
	done
	for f in total_ms draw_ms present_ms; do
		printf 'M2      %-10s ' "$f"
		frames "select(.cause == \"key\") | .$f" | pct
	done
	# Ready for the first step: the end of the first frame after the one of
	# the activation, the Expose that mapping causes
	printf 'M1 ready           '
	jq -rs 'map(select(.message == "Frame")) | group_by(.activation)[]
		| map(select(.cause == "event" or .cause == "refresh"))[0].activation_ms // empty' \
		"$out/log.json" | pct

	# glFinish runs only while timings are logged, and NVIDIA busy-waits in it:
	# its samples are not CPU work of a frame
	if [[ -f $out/cpu.prof && -f $out/mem.prof ]]; then
		n=$(frames 'select(.cause != "refresh") | .cause' | wc -l)
		cpu=$(go tool pprof -top -cum -unit=ms -ignore='_Cfunc_glowFinish' "$out/qws" "$out/cpu.prof" 2>/dev/null |
			awk -v f="$render_fn" '$NF == f { sub(/ms$/, "", $4); print $4 }')
		alloc=$(go tool pprof -sample_index=alloc_space -top -cum -unit=B "$out/qws" "$out/mem.prof" 2>/dev/null |
			awk -v f="$render_fn" '$NF == f { sub(/B$/, "", $4); print $4 }')
		awk -v n="$n" -v cpu="${cpu:-0}" -v alloc="${alloc:-0}" 'BEGIN {
			printf "M3 frames=%d render_cpu_ms=%s cpu_ms_per_frame=%.2f alloc_mb_per_frame=%.2f\n",
				n, cpu, cpu / n, alloc / n / 1048576 }'
	fi

	grep -q '"message":"Animation frame"' "$out/log.json" && animations "$out"
	return 0
}

# Metrics A1–A3 and criteria K2, K4, K5 of specs/007-animation
animations() {
	local out=$1 kind keys
	anim() { jq -r "select(.message == \"Animation frame\") | $1" "$out/log.json"; }
	keys=$(sed -n 's/^keys per activation: //p' "$out/meta")

	for kind in $(anim .kind | sort -u); do
		printf 'A1 %-5s interval   ' "$kind"
		anim "select(.kind == \"$kind\" and .interval_ms) | .interval_ms" | pct
		printf 'A1 %-5s missed     ' "$kind"
		anim "select(.kind == \"$kind\" and .interval_ms) | .interval_ms > 1.5 * .period_ms" |
			awk '{ n++; m += ($1 == "true") } END { printf "%d of %d intervals above 1.5 periods\n", m, n }'
	done
	printf 'A2 response        '
	anim 'select(.response_ms and (.kind == "carousel" or .kind == "grid")) | .response_ms' | pct
	for kind in $(anim .kind | sort -u); do
		printf 'A3 %-8s duration ' "$kind"
		anim "select(.kind == \"$kind\" and .at_rest and .retargets == 0) | .duration_ms" | pct
		printf 'K2 %-8s frames   ' "$kind"
		jq -rs --arg kind "$kind" 'map(select(.message == "Animation frame" and .kind == $kind))
			| group_by([.activation, .animation])[]
			| select(all(.retargets == 0) and any(.at_rest)) | length' "$out/log.json" | pct
	done
	printf 'K4 period          '
	anim 'select(.at_rest) | .period_ms' | pct
	printf 'K6 activation      '
	jq -r 'select(.message == "Window activated") | .since_choice_ms' "$out/log.json" | pct
	if [[ -f $out/k5 ]]; then
		# The step of the last held key: at rest since its key, and where the
		# keys lead
		printf 'K5 at rest         '
		jq -rs 'map(select(.message == "Animation frame" and .at_rest and .retargets > 0))
			| group_by(.activation)[] | last | .since_key_ms' "$out/log.json" | pct
		awk '{ n++; sub(/,$/, "", $4) } $4 != $6 { bad++; print "K5 " $0 }
			END { printf "K5 selection       %d of %d activations off\n", bad, n }' "$out/k5"
	fi
}

case ${1:-} in
run) run "$2" "$3" ;;
summary) summary "$2" ;;
k3) k3 "$2" ;;
*) sed -n '2,21p' "$0" >&2; exit 2 ;;
esac
