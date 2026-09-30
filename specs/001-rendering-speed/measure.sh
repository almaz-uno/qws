#!/usr/bin/env bash
# Session S1 of specs/001-rendering-speed: drives a measuring qws instance with
# xdotool and summarises its frame timings and profiles.
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
		echo "i3 windows: $(focus_order | wc -l)"
		echo "activations: $ACTIVATIONS, steps: $STEPS"
	} >"$out/meta"
	cp "$qws" "$out/qws"

	env 'QWS_LOG.FORMAT=json' "$out/qws" -v -r "$renderer" -m Alt -k "$KEY" \
		--behavior-show-delay 0 \
		--cpuprofile "$out/cpu.prof" --memprofile "$out/mem.prof" \
		2>"$log" &
	pid=$!
	trap 'xdotool keyup alt; kill -INT '"$pid"' 2>/dev/null || true' EXIT
	sleep 1
	kill -0 "$pid"

	warm_up

	for ((a = 1; a <= ACTIVATIONS; a++)); do
		# The first frame, then the frame of the Expose that mapping causes
		n=$(frame_count "$log")
		xdotool keydown alt key "$KEY"
		wait_frames "$log" $((n + 2)) || true
		for ((s = 1; s <= STEPS; s++)); do
			n=$(frame_count "$log")
			xdotool key Right
			wait_frames "$log" $((n + 1)) || true
		done
		xdotool key Escape keyup alt
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
		env 'QWS_LOG.FORMAT=json' "$qws" -v -r "$renderer" -m Alt -k "$KEY" \
			--debug-dump-frames "$out/$renderer" 2>"$log" &
		pid=$!
		trap 'xdotool keyup alt; kill -INT '"$pid"' 2>/dev/null || true' EXIT
		sleep 1
		kill -0 "$pid"

		for ((a = 1; a <= 3; a++)); do
			n=$(frame_count "$log")
			xdotool keydown alt key "$KEY"
			wait_frames "$log" $((n + 2)) || true
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
	n=$(frames 'select(.cause != "refresh") | .cause' | wc -l)
	cpu=$(go tool pprof -top -cum -unit=ms -ignore='_Cfunc_glowFinish' "$out/qws" "$out/cpu.prof" 2>/dev/null |
		awk -v f="$render_fn" '$NF == f { sub(/ms$/, "", $4); print $4 }')
	alloc=$(go tool pprof -sample_index=alloc_space -top -cum -unit=B "$out/qws" "$out/mem.prof" 2>/dev/null |
		awk -v f="$render_fn" '$NF == f { sub(/B$/, "", $4); print $4 }')
	awk -v n="$n" -v cpu="${cpu:-0}" -v alloc="${alloc:-0}" 'BEGIN {
		printf "M3 frames=%d render_cpu_ms=%s cpu_ms_per_frame=%.2f alloc_mb_per_frame=%.2f\n",
			n, cpu, cpu / n, alloc / n / 1048576 }'
}

case ${1:-} in
run) run "$2" "$3" ;;
summary) summary "$2" ;;
k3) k3 "$2" ;;
*) sed -n '2,16p' "$0" >&2; exit 2 ;;
esac
