#!/usr/bin/env bash
set -euo pipefail

#
# migrate-v2-checkpoints-to-v1.sh - Inspect legacy v2 checkpoints for v1 migration.
#
# USAGE:
#   ./scripts/migrate-v2-checkpoints-to-v1.sh [OPTIONS] [SINCE_COMMIT]
#
# OPTIONS:
#   -h, --help            Show this help message
#   --list                Print checkpoint IDs and associated commit IDs only
#   --dry-run             Print every v2 folder/file that would be migrated
#   --apply               Build migration commit support path (currently blocked before writing)
#   --repo <path>         Local repository path to inspect
#   --since <commit>      Commit before the checkpoints to inspect
#   --head <commit>       Limit scan to one history tip (default: all branches/remotes)
#
# DESCRIPTION:
#   Read-only first pass for converting legacy checkpoints v2 data back to the
#   v1 checkpoint format. The script finds commits newer than SINCE_COMMIT on
#   local branches/remotes (or on --head, when supplied), extracts
#   Entire-Checkpoint trailers, and prints the v2 /full folders/files that
#   contain raw transcripts:
#
#     refs/entire/checkpoints/v2/full/*:<checkpoint-path>/<session>/raw_transcript*
#
#   When refs/entire/checkpoints/v2/main is available, companion checkpoint and
#   session metadata paths are printed after the full transcript folders.
#
#   If --repo or SINCE_COMMIT is omitted, the script prompts for it.
#

V1_REF="refs/heads/entire/checkpoints/v1"
V2_MAIN_REF="refs/entire/checkpoints/v2/main"
V2_FULL_REF_PREFIX="refs/entire/checkpoints/v2/full"
TRAILER_KEY="Entire-Checkpoint"

since_commit=""
head_commitish=""
repo_path=""
dry_run=false
apply=false
list_mode=false
plan_entries_file=""

show_help() {
	sed -n '3,/^$/p' "$0" | sed -E 's/^# ?//'
}

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

warn() {
	printf 'warning: %s\n' "$*" >&2
}

cleanup() {
	if [[ -n "$plan_entries_file" && -f "$plan_entries_file" ]]; then
		rm -f "$plan_entries_file"
	fi
}
trap cleanup EXIT

checkpoint_to_path() {
	local checkpoint_id="$1"
	printf '%s/%s' "${checkpoint_id:0:2}" "${checkpoint_id:2}"
}

tree_path_exists() {
	local ref_name="$1"
	local path="$2"
	git cat-file -e "${ref_name}:${path}" 2>/dev/null
}

list_numeric_dirs() {
	local ref_name="$1"
	local path="$2"
	local entries
	entries=$(git ls-tree -d --name-only "${ref_name}:${path}" 2>/dev/null || true)
	printf '%s\n' "$entries" | sed -nE '/^[0-9]+$/p'
}

list_full_refs() {
	git for-each-ref --format='%(refname)' "$V2_FULL_REF_PREFIX" | sort
}

list_checkpoint_ids_between() {
	local since="$1"
	local head="$2"
	git log --format=%B "${since}..${head}" |
		sed -nE "s/^[[:space:]]*${TRAILER_KEY}:[[:space:]]*([0-9a-f]{12})[[:space:]]*$/\\1/p" |
		awk '!seen[$0]++'
}

list_checkpoint_ids_from_all_refs() {
	local since="$1"
	git log HEAD --branches --remotes --format=%B --not "$since" |
		sed -nE "s/^[[:space:]]*${TRAILER_KEY}:[[:space:]]*([0-9a-f]{12})[[:space:]]*$/\\1/p" |
		awk '!seen[$0]++'
}

list_commit_ids_for_checkpoint() {
	local checkpoint_id="$1"
	local since="$2"
	local head="$3"
	local commits

	if [[ -n "$head" ]]; then
		commits=$(git log --format=%H --extended-regexp \
			--grep="^${TRAILER_KEY}:[[:space:]]*${checkpoint_id}[[:space:]]*$" \
			"${since}..${head}")
	else
		commits=$(git log HEAD --branches --remotes --format=%H --extended-regexp \
			--grep="^${TRAILER_KEY}:[[:space:]]*${checkpoint_id}[[:space:]]*$" \
			--not "$since")
	fi

	printf '%s\n' "$commits" | awk 'NF && !seen[$0]++'
}

list_full_artifacts() {
	local ref_name="$1"
	local session_path="$2"
	local entries
	entries=$(git ls-tree --name-only "${ref_name}:${session_path}" 2>/dev/null || true)
	printf '%s\n' "$entries" |
		sed -nE '/^raw_transcript(\.[0-9]+)?$/p; /^raw_transcript_hash\.txt$/p'
}

v1_raw_artifact_name() {
	local artifact="$1"
	case "$artifact" in
		raw_transcript)
			printf 'full.jsonl'
			;;
		raw_transcript.[0-9][0-9][0-9])
			printf 'full.jsonl%s' "${artifact#raw_transcript}"
			;;
		raw_transcript_hash.txt)
			printf 'content_hash.txt'
			;;
		*)
			return 1
			;;
	esac
}

append_tree_entry_from_source() {
	local source_ref="$1"
	local source_path="$2"
	local target_path="$3"
	local entry meta mode type hash

	entry=$(git ls-tree "$source_ref" -- "$source_path" || true)
	[[ -n "$entry" ]] || return 1

	meta=${entry%%$'\t'*}
	read -r mode type hash <<< "$meta"
	[[ "$type" == "blob" && -n "$hash" ]] || return 1

	printf '%s %s %s\t%s\n' "$mode" "$type" "$hash" "$target_path" >> "$plan_entries_file"
}

write_unique_mktree_input() {
	local entries_file="$1"
	awk -F '\t' 'NF >= 2 { line[$2] = $0 } END { for (path in line) print line[path] }' "$entries_file" |
		sort -k2
}

build_v1_tree_from_plan() {
	local entries_file="$1"
	local combined
	combined=$(mktemp "${TMPDIR:-/tmp}/v2-to-v1-tree.XXXXXX")
	if git show-ref --verify --quiet "$V1_REF"; then
		git ls-tree -r "$V1_REF" > "$combined"
	else
		: > "$combined"
	fi
	cat "$entries_file" >> "$combined"
	write_unique_mktree_input "$combined" | git mktree
	rm -f "$combined"
}

create_v1_migration_commit() {
	local entries_file="$1"
	local tree_hash parent_hash commit_hash

	tree_hash=$(build_v1_tree_from_plan "$entries_file")
	if git show-ref --verify --quiet "$V1_REF"; then
		parent_hash=$(git rev-parse "$V1_REF^{commit}")
		commit_hash=$(printf 'Migrate checkpoints v2 to v1\n\nSource refs: %s and %s/*\n' "$V2_MAIN_REF" "$V2_FULL_REF_PREFIX" |
			git commit-tree "$tree_hash" -p "$parent_hash")
	else
		commit_hash=$(printf 'Migrate checkpoints v2 to v1\n\nSource refs: %s and %s/*\n' "$V2_MAIN_REF" "$V2_FULL_REF_PREFIX" |
			git commit-tree "$tree_hash")
	fi

	printf '%s\n' "$commit_hash"
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		-h|--help)
			show_help
			exit 0
			;;
		--list)
			list_mode=true
			shift
			;;
		--dry-run)
			dry_run=true
			shift
			;;
		--apply)
			apply=true
			shift
			;;
		--since)
			[[ $# -ge 2 ]] || die "--since requires a commit"
			since_commit="$2"
			shift 2
			;;
		--repo)
			[[ $# -ge 2 ]] || die "--repo requires a path"
			repo_path="$2"
			shift 2
			;;
		--head)
			[[ $# -ge 2 ]] || die "--head requires a commit"
			head_commitish="$2"
			shift 2
			;;
		-*)
			die "unknown option: $1"
			;;
		*)
			[[ -z "$since_commit" ]] || die "too many commit arguments"
			since_commit="$1"
			shift
			;;
	esac
done

mode_count=0
[[ "$list_mode" == "true" ]] && mode_count=$((mode_count + 1))
[[ "$dry_run" == "true" ]] && mode_count=$((mode_count + 1))
[[ "$apply" == "true" ]] && mode_count=$((mode_count + 1))
if (( mode_count > 1 )); then
	die "--list, --dry-run, and --apply are mutually exclusive"
fi

plan_entries_file=$(mktemp "${TMPDIR:-/tmp}/v2-to-v1-plan.XXXXXX")

if [[ -z "$repo_path" ]]; then
	printf 'Local repo path: ' >&2
	IFS= read -r repo_path
fi

[[ -n "$repo_path" ]] || die "a local repo path is required"
[[ -d "$repo_path" ]] || die "repo path does not exist or is not a directory: $repo_path"

if ! repo_root=$(git -C "$repo_path" rev-parse --show-toplevel 2>/dev/null); then
	die "not inside a git repository: $repo_path"
fi
cd "$repo_root"

if [[ -z "$since_commit" ]]; then
	printf 'Show v2 checkpoints newer than commit: ' >&2
	IFS= read -r since_commit
fi

[[ -n "$since_commit" ]] || die "a base commit is required"

if ! since_hash=$(git rev-parse --verify --quiet "${since_commit}^{commit}"); then
	die "commit not found: $since_commit"
fi

head_hash=""
if [[ -n "$head_commitish" ]]; then
	if ! head_hash=$(git rev-parse --verify --quiet "${head_commitish}^{commit}"); then
		die "history tip not found: $head_commitish"
	fi
	git merge-base --is-ancestor "$since_hash" "$head_hash" 2>/dev/null ||
		die "$since_commit is not an ancestor of $head_commitish"
fi

main_ref_available=false
if git show-ref --verify --quiet "$V2_MAIN_REF"; then
	main_ref_available=true
else
	warn "missing $V2_MAIN_REF; companion metadata paths will not be shown"
fi

full_refs=$(list_full_refs)
[[ -n "$full_refs" ]] || die "missing refs under $V2_FULL_REF_PREFIX; cannot locate raw transcripts"

if [[ -n "$head_hash" ]]; then
	checkpoint_ids=$(list_checkpoint_ids_between "$since_hash" "$head_hash")
else
	checkpoint_ids=$(list_checkpoint_ids_from_all_refs "$since_hash")
fi
if [[ -z "$checkpoint_ids" ]]; then
	if [[ -n "$head_hash" ]]; then
		printf 'No %s trailers found in %s..%s\n' "$TRAILER_KEY" "$since_hash" "$head_hash"
	else
		printf 'No %s trailers found on local branches/remotes after %s\n' "$TRAILER_KEY" "$since_hash"
	fi
	exit 0
fi

if [[ "$list_mode" == "true" ]]; then
	checkpoint_count=$(printf '%s\n' "$checkpoint_ids" | sed '/^$/d' | wc -l | tr -d '[:space:]')
	printf 'Checkpoints: %s\n' "$checkpoint_count"
	printf 'checkpoint_id\tcommit_ids\n'
	while IFS= read -r checkpoint_id; do
		[[ -n "$checkpoint_id" ]] || continue
		commits=$(list_commit_ids_for_checkpoint "$checkpoint_id" "$since_hash" "$head_hash" | tr '\n' ' ' | sed 's/[[:space:]]*$//')
		printf '%s\t' "$checkpoint_id"
		if [[ -n "$commits" ]]; then
			printf '%s' "$commits"
		fi
		printf '\n'
	done <<< "$checkpoint_ids"
	exit 0
fi

printf 'Repository: %s\n' "$repo_root"
if [[ -n "$head_hash" ]]; then
	printf 'Scanning commits: %s..%s\n' "$since_hash" "$head_hash"
else
	printf 'Scanning commits: local branches/remotes after %s\n' "$since_hash"
fi
if [[ "$main_ref_available" == "true" ]]; then
	printf 'Companion metadata ref: %s\n' "$V2_MAIN_REF"
fi
printf 'Full refs:\n'
printf '%s\n' "$full_refs" | sed 's/^/  /'
printf '\n'

planned_checkpoints=0
planned_sessions=0
planned_raw_transcripts=0
missing_raw_checkpoints=0
missing_metadata_checkpoints=0
missing_metadata_sessions=0

while IFS= read -r checkpoint_id; do
	[[ -n "$checkpoint_id" ]] || continue

	checkpoint_path=$(checkpoint_to_path "$checkpoint_id")

	found_full_artifact=false
	found_sessions=""
	checkpoint_output=""
	while IFS= read -r full_ref; do
		[[ -n "$full_ref" ]] || continue

		sessions=$(list_numeric_dirs "$full_ref" "$checkpoint_path")
		printed_full_checkpoint=false
		while IFS= read -r session_index; do
			[[ -n "$session_index" ]] || continue
			session_path="$checkpoint_path/$session_index"
			artifacts=$(list_full_artifacts "$full_ref" "$session_path")
			if [[ -z "$artifacts" ]]; then
				continue
			fi

			found_full_artifact=true
			found_sessions="${found_sessions}${session_index}"$'\n'
			if [[ "$printed_full_checkpoint" != "true" ]]; then
				printed_full_checkpoint=true
				checkpoint_output="${checkpoint_output}  full checkpoint folder: ${full_ref}:${checkpoint_path}"$'\n'
			fi
			checkpoint_output="${checkpoint_output}  full session folder: ${full_ref}:${session_path}"$'\n'
			while IFS= read -r artifact; do
				[[ -n "$artifact" ]] || continue
				checkpoint_output="${checkpoint_output}    raw artifact: ${full_ref}:${session_path}/${artifact}"$'\n'
			done <<< "$artifacts"
		done <<< "$sessions"
	done <<< "$full_refs"

	if [[ "$found_full_artifact" != "true" ]]; then
		warn "no raw_transcript artifacts found for checkpoint $checkpoint_id"
		missing_raw_checkpoints=$((missing_raw_checkpoints + 1))
		continue
	fi

	planned_checkpoints=$((planned_checkpoints + 1))

	if [[ "$main_ref_available" == "true" ]] && tree_path_exists "$V2_MAIN_REF" "$checkpoint_path"; then
		checkpoint_output="${checkpoint_output}  companion metadata folder: ${V2_MAIN_REF}:${checkpoint_path}"$'\n'
		if tree_path_exists "$V2_MAIN_REF" "$checkpoint_path/metadata.json"; then
			checkpoint_output="${checkpoint_output}    checkpoint metadata: ${V2_MAIN_REF}:${checkpoint_path}/metadata.json"$'\n'
			append_tree_entry_from_source "$V2_MAIN_REF" "$checkpoint_path/metadata.json" "$checkpoint_path/metadata.json" ||
				warn "failed to plan checkpoint metadata for $checkpoint_id"
		fi

		metadata_sessions=$(printf '%s' "$found_sessions" | sed '/^$/d' | sort -n | uniq)
		while IFS= read -r session_index; do
			[[ -n "$session_index" ]] || continue
			session_path="$checkpoint_path/$session_index"
			planned_sessions=$((planned_sessions + 1))
			if tree_path_exists "$V2_MAIN_REF" "$session_path/metadata.json"; then
				checkpoint_output="${checkpoint_output}    session metadata: ${V2_MAIN_REF}:${session_path}/metadata.json"$'\n'
				append_tree_entry_from_source "$V2_MAIN_REF" "$session_path/metadata.json" "$session_path/metadata.json" ||
					warn "failed to plan session metadata for checkpoint $checkpoint_id session $session_index"
			else
				missing_metadata_sessions=$((missing_metadata_sessions + 1))
				warn "missing session metadata for checkpoint $checkpoint_id session $session_index on $V2_MAIN_REF"
			fi
			if tree_path_exists "$V2_MAIN_REF" "$session_path/prompt.txt"; then
				checkpoint_output="${checkpoint_output}    prompt: ${V2_MAIN_REF}:${session_path}/prompt.txt"$'\n'
				append_tree_entry_from_source "$V2_MAIN_REF" "$session_path/prompt.txt" "$session_path/prompt.txt" ||
					warn "failed to plan prompt for checkpoint $checkpoint_id session $session_index"
			fi
		done <<< "$metadata_sessions"
	elif [[ "$main_ref_available" == "true" ]]; then
		missing_metadata_checkpoints=$((missing_metadata_checkpoints + 1))
		warn "checkpoint $checkpoint_id has raw transcript artifacts but no companion metadata on $V2_MAIN_REF"
	fi

	while IFS=$'\t' read -r full_ref session_index artifact; do
		[[ -n "${full_ref:-}" && -n "${session_index:-}" && -n "${artifact:-}" ]] || continue
		session_path="$checkpoint_path/$session_index"
		v1_artifact=$(v1_raw_artifact_name "$artifact") || continue
		append_tree_entry_from_source "$full_ref" "$session_path/$artifact" "$session_path/$v1_artifact" ||
			warn "failed to plan raw artifact for checkpoint $checkpoint_id session $session_index: $artifact"
		if [[ "$artifact" == "raw_transcript" ]]; then
			planned_raw_transcripts=$((planned_raw_transcripts + 1))
		fi
	done < <(
		while IFS= read -r full_ref; do
			[[ -n "$full_ref" ]] || continue
			sessions=$(list_numeric_dirs "$full_ref" "$checkpoint_path")
			while IFS= read -r session_index; do
				[[ -n "$session_index" ]] || continue
				session_path="$checkpoint_path/$session_index"
				artifacts=$(list_full_artifacts "$full_ref" "$session_path")
				while IFS= read -r artifact; do
					[[ -n "$artifact" ]] || continue
					printf '%s\t%s\t%s\n' "$full_ref" "$session_index" "$artifact"
				done <<< "$artifacts"
			done <<< "$sessions"
		done <<< "$full_refs"
	)

	if [[ "$dry_run" == "true" ]]; then
		printf 'checkpoint %s\n' "$checkpoint_id"
		printf '%s' "$checkpoint_output"
		printf '\n'
	fi
done <<< "$checkpoint_ids"

planned_entries=$(wc -l < "$plan_entries_file" | tr -d '[:space:]')
unique_planned_entries=$(write_unique_mktree_input "$plan_entries_file" | wc -l | tr -d '[:space:]')

if [[ "$dry_run" != "true" ]]; then
	printf 'Migration plan:\n'
	printf '  target ref: %s\n' "$V1_REF"
	printf '  checkpoints with raw transcripts: %s\n' "$planned_checkpoints"
	printf '  sessions with raw transcripts: %s\n' "$planned_sessions"
	printf '  raw transcript base files: %s\n' "$planned_raw_transcripts"
	printf '  planned v1 tree entries: %s (%s unique target paths)\n' "$planned_entries" "$unique_planned_entries"
	printf '  missing raw-transcript checkpoints: %s\n' "$missing_raw_checkpoints"
	printf '  missing companion checkpoint metadata: %s\n' "$missing_metadata_checkpoints"
	printf '  missing companion session metadata: %s\n' "$missing_metadata_sessions"
	printf '\n'
fi

if [[ "$apply" == "true" ]]; then
	# This is the final write path, intentionally blocked until the migration
	# behavior is reviewed with real repo output. It reuses v2 blob objects,
	# builds one complete v1 tree, and would then update V1_REF to the commit.
	die "--apply is scaffolded but intentionally blocked before creating a migration commit or updating $V1_REF"
fi

if [[ "$dry_run" != "true" ]]; then
	printf 'No refs were written. Use --dry-run to print every source and target artifact.\n'
	printf 'The single-commit apply path is scaffolded but blocked before updating %s.\n' "$V1_REF"
fi
