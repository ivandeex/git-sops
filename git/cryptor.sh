#!/bin/bash
#set -x
set -euo pipefail

script_version='2.0.0-iva02'
script_path=$(readlink -f "$0")
prog=$(basename "$script_path")

tempdir=${XDG_RUNTIME_DIR:-/tmp}
filtering="${IVACRYPT_FILTERING:-default}"
is_binary='true'
merge_mode='binary'
cache_textconv='true'

action="${1:-unknown}"
filename="${2:-}"

# WARNING: PBKDF2 iterations are incompatible with pre-1.1.1 OpenSSL!
openssl111_iterations=0
#openssl111_iterations=1000

die() {
    echo "error: $*" 1>&2
    exit 1
}

setup_encoding() {
    cipher=$(git config --get --local ivacrypt.cipher || true)
    password=$(git config --get --local ivacrypt.password || true)

    salt=''
    if [ "$1" = 'salt' ]; then
        salt=$(openssl dgst -hmac "${filename}:${password}" -sha256 "$filename" | tr -d '\r\n' | tail -c 16)
    fi
    cipher=${2:-$cipher}
    password=${3:-$password}
    export password

    enc_args="enc -$cipher -md MD5 -pass env:password -a"
    if [ $openssl111_iterations -gt 0 ]; then
        enc_args="$enc_args -iter $openssl111_iterations"
        if [ "$openssl111_found" = 'n' ]; then
            openssl version | grep -Eq '^OpenSSL (1\.1\.[^0]|1\.[^01]|[^1]\.)' || die 'openssl 1.1.1+ required'
            openssl111_found='y'
        fi
    fi
}
openssl111_found='n'
warning_regex='(deprecated key derivation|-iter or -pbkdf2 would be better)'

filter_clean() {
    [ -s "$filename" ] || exit 0  # ignore empty files

    # cache STDIN to test if it's already encrypted
    tempfile=$(mktemp "$tempdir/.ivacrypt-clean.XXXXXXXX")
    trap 'rm -f "$tempfile"' EXIT
    cat > "$tempfile"

    # the first bytes of an encrypted file are always "Salted" in Base64
    read -r -n 8 firstbytes <"$tempfile" || true  # fails if file's too small
    [ "$firstbytes" = 'U2FsdGVk' ] && encrypted='y' || encrypted='n'

    case "$filtering" in
      encrypt)
        the_case="$encrypted,y" ;;
      decrypt)
        the_case="$encrypted,n" ;;
      none)
        the_case="n,n" ;;
      *)
        get_status
        [[ $secret_status = true ]] && the_case="$encrypted,y" || the_case="$encrypted,n"
    esac

    case "$the_case" in
      n,y)
        setup_encoding salt
        # shellcheck disable=SC2086
        openssl $enc_args -e -S "$salt" -in "$tempfile" 2> >(grep -Ev "$warning_regex" >&2)
        ;;
      y,n)
        setup_encoding nosalt
        # shellcheck disable=SC2086
        openssl $enc_args -d -in "$tempfile" 2>/dev/null || cat "$tempfile"
        ;;
      *)
        cat "$tempfile"
        ;;
    esac
    exit 0
}

filter_smudge() {
    [ "$filtering" != 'none' ] || exec cat

    # the first bytes of an encrypted file are always "Salted" in Base64
    tempfile=$(mktemp "$tempdir/.ivacrypt-smudge.XXXXXXXX")
    trap 'rm -f "$tempfile"' EXIT
    cat > "$tempfile"

    setup_encoding nosalt
    # shellcheck disable=SC2086
    openssl $enc_args -d -in "$tempfile" 2>/dev/null || cat "$tempfile"
    exit 0
}

filter_textconv() {
    [ -s "$filename" ] || exit 0  # ignore empty files
    [ "$filtering" != 'none' ] || exec cat "$filename"
    setup_encoding nosalt
    # shellcheck disable=SC2086
    openssl $enc_args -d -in "$filename" 2>/dev/null || exec cat "$filename"
    exit 0
}

merge_driver() {
    ancestor=$1
    current=$2
    other=$3
    filename=$4

    conflict_style=$(git config --get --local merge.conflictstyle || true)
    [ "$conflict_style" = 'diff3' ] && diff_opt='--diff3' || diff_opt='--no-diff3'

    case "$filtering" in
      encrypt)
        encrypt_result='y' ;;
      decrypt|none)
        encrypt_result='n' ;;
      *)
        get_status
        [[ $secret_status = true ]] && encrypt_result='y' || encrypt_result='n'
    esac

    setup_encoding salt

    plain_ancestor=$(mktemp "$tempdir/.ivacrypt-merge_ancestor.XXXXXXXX")
    plain_current=$(mktemp "$tempdir/.ivacrypt-merge_current.XXXXXXXX")
    plain_other=$(mktemp "$tempdir/.ivacrypt-merge_other.XXXXXXXX")
    trap 'rm -f "$plain_ancestor" "$plain_current" "$plain_other"' EXIT

    if [ "$filtering" != 'none' ] && [ -s "$ancestor" ]; then
        # shellcheck disable=SC2086
        openssl $enc_args -d -in "$ancestor" -out "$plain_ancestor" 2>/dev/null || cp "$ancestor" "$plain_ancestor"
    else
        cp "$ancestor" "$plain_ancestor"
    fi
    if [ "$filtering" != 'none' ] && [ -s "$current" ]; then
        # shellcheck disable=SC2086
        openssl $enc_args -d -in "$current" -out "$plain_current" 2>/dev/null || cp "$current" "$plain_current"
    else
        cp "$current" "$plain_current"
    fi
    if [ "$filtering" != 'none' ] && [ -s "$other" ]; then
        # shellcheck disable=SC2086
        openssl $enc_args -d -in "$other" -out "$plain_other" 2>/dev/null || cp "$other" "$plain_other"
    else
        cp "$other" "$plain_other"
    fi

    set +e
    git merge-file -L 'CURRENT' -L 'ANCESTOR' -L 'OTHER' $diff_opt "$plain_current" "$plain_ancestor" "$plain_other"
    exit_code=$?
    set -e

    if [ $encrypt_result = 'y' ]; then
        # shellcheck disable=SC2086
        openssl $enc_args -e -S "$salt" -in "$plain_current" -out "$current" 2> >(grep -Ev "$warning_regex" >&2)
    else
        cp "$plain_current" "$current"
    fi

    exit $exit_code
}

convert_branch() {
    mode="$1"
    branch="$2"
    case "${3:-}" in
      -f|--force)
        force='force'
        ;;
      '')
        force=''
        ;;
      *)
        usage
        ;;
    esac

    IVACRYPT_DIR=$(git rev-parse --show-toplevel)
    export IVACRYPT_DIR

    script_dir=$(dirname "$script_path")
    if [ "$script_dir" != '/tmp' ]; then
        safety_checks 'noforce' 'true'
        runner=/tmp/.ivacrypt-runner.${script_path//\//-}
        echo "Running safe copy of the script"
        cp "$script_path" "$runner"
        chmod 750 "$runner"
        exec "$runner" "${mode}" "$branch"
    fi
    # shellcheck disable=SC2015
    cd "$IVACRYPT_DIR" || die 'wrong git directory'
    safety_checks 'noforce' 'true'

    cur_branch=$(git rev-parse --symbolic --abbrev-ref HEAD 2>/dev/null || true)
    [[ $cur_branch ]] || die 'not on branch'
    branch=${branch:-$cur_branch}

    if [ "$branch" != "$cur_branch" ]; then
        if git show-ref --verify --quiet "refs/heads/$branch"; then
            if [[ $force ]]; then
                git branch -D "$branch"
            else
                die "branch $branch already exists"
	    fi
        fi
        git checkout -b "$branch" || die "cannot create branch $branch"
    fi

    echo "'$branch' $mode in progress..."
    mark_branch "$branch" "$mode" 'just_mark'

    fb_temp=$(mktemp -d "$tempdir/.ivacrypt-branch.XXXXXXXX")
    trap 'on_convert_done "$branch" "$mode" "$fb_temp"' EXIT

    # remove old junk, purge textconv cache
    git update-ref -d "refs/original/refs/heads/$branch"
    rm -rf "$IVACRYPT_DIR/.git-rewrite" "$fb_temp"
    purge_cache
    #git config --local diff.crypt.cachetextconv 'false' || true

    export IVACRYPT_FILTERING=$mode
    export FILTER_BRANCH_SQUELCH_WARNING=1
    git filter-branch -f -d "$fb_temp" --prune-empty --index-filter "'$script_path' on-convert-commit" --setup 'git reset --hard'
    result=$?
    if [ "${script_path#/tmp/.ivacrypt-runner.}" != "$script_path" ]; then
        rm -f "$script_path"
    fi
    exit $result
}

on_convert_commit() {
    files=$(ls_crypt)
    if [[ $files ]]; then
        git checkout-index --force --index --stdin <<< "$files"
        xargs touch <<< "$files"
        git update-index --add --replace --remove --stdin <<< "$files"
    fi
}

on_convert_done() {
    branch="$1"
    mode="$2"
    fb_temp="$3"

    # remove leftovers
    rm -rf "$IVACRYPT_DIR/.git-rewrite" "$fb_temp"
    #git config --local diff.crypt.cachetextconv 'true' || true

    mark_branch "$branch" "$mode" 'pushes'
    echo "$branch: $mode complete"
}

mark_branch() {
    branch="$1"
    mode="$2"
    pushes="$3"

    [[ $branch ]] || branch=$(git rev-parse --symbolic --abbrev-ref HEAD 2>/dev/null || true)
    [[ $branch ]] || die 'not on branch'

    case "$mode" in
      encrypt)
        # remove "decrypt" marker
        git config --unset --local "branch.$branch.ivacrypt-status" || true
        ;;
      decrypt)
        # mark branch as decrypted for filters
        git config --local "branch.$branch.ivacrypt-status" decrypt
        ;;
    esac

    case "${mode},${pushes}" in
      encrypt,pushes)
        # re-enable pushes on branch
        saved_remote=$(git config --get --local "branch.$branch.ivacrypt-saved-remote" || true)
        git config --unset --local "branch.$branch.ivacrypt-saved-remote" || true
        if [[ $saved_remote ]] && [ "$saved_remote" != 'ivacrypt-push-disabled' ]; then
            git config --local "branch.$branch.remote" "$saved_remote"
        else
            git config --unset --local "branch.$branch.remote" || true
            remove_if_empty "branch.$branch"
        fi
        ;;
      decrypt,pushes)
        # disable pushes on branch
        saved_remote=$(git config --get --local "branch.$branch.remote" || true)
        if [[ $saved_remote ]] && [ "$saved_remote" != 'ivacrypt-push-disabled' ]; then
            git config --local "branch.$branch.ivacrypt-saved-remote" "$saved_remote"
        else
            git config --unset --local "branch.$branch.ivacrypt-saved-remote" || true
        fi
        git config --local "branch.$branch.remote" 'ivacrypt-push-disabled'
        ;;
    esac
}

remove_if_empty() {
    section=$1
    regex="${section//./\\.}\\..*"
    values=$(git config --get-regex --local "$regex" || true)
    [[ $values ]] || git config --remove-section --local "$section" 2>/dev/null || true
}

ls_crypt() {
    git ls-files | git check-attr --stdin filter | awk -F: '/crypt$/ {print $1}'
}

chmod_crypt() {
    cd "$(git rev-parse --show-toplevel)" || die 'not in a repository'
    ls_crypt | xargs -r chmod go-rwx
}

purge_cache() {
    git update-ref -d refs/notes/textconv/crypt
}

raw_log() {
    trap 'git config --local diff.crypt.binary $is_binary' EXIT
    git config --local diff.crypt.binary 'false'
    purge_cache

    export IVACRYPT_FILTERING='none'
    format='%C(bold blue)%h%C(reset) - %C(white)%s%C(reset)%C(bold yellow)%d%C(reset)'
    git log --patch --no-textconv --abbrev-commit --decorate --date=relative --format="format:$format" "$@"
}

get_status() {
    configured=$(git config --get --local "ivacrypt.configured" || true)
    branch=$(git rev-parse --symbolic --abbrev-ref HEAD 2>/dev/null || true)
    rebase_head="$(git rev-parse --git-dir)/rebase-merge/head-name"
    rebase_status='false'
    if [ "$branch" = 'HEAD' ] && [ -r "$rebase_head" ]; then
        branch=$(cut -d/ -f3- "$rebase_head")
        rebase_status='true'
    fi
    status=$(git config --get --local "branch.$branch.ivacrypt-status" || true)
    if [ "$configured" = 'true' ] && [ "$status" != 'decrypt' ]; then
        secret_status='true'
        status='encrypt'
    else
        secret_status='false'
        status='decrypt'
    fi
}

check_status() {
    get_status
    echo "setup:  ${configured:-false}"
    echo "branch: ${branch:-NONE}"
    echo "secret: ${secret_status}"
    echo "rebase: ${rebase_status}"
}

force_checkout() {
    for file in $(ls_crypt); do
        rm -f "$file"
        git checkout --force HEAD -- "$file"
    done
}

setup_repo() {
    cipher=''
    password=''
    testfile=''
    testdata=''
    force=''
    while [[ "${1:-}" != '' ]]; do
        case "$1" in
          -c|--cipher)
            cipher=${2:-}
            shift
            ;;
          -p|--password)
            password=$(get_cli_secret 'password' "${2:-}")
            shift
            [ "$(wc -l <<< "$password")" = 1 ] || die "password cannot be multiline"
            ;;
          -t|--testfile)
            testfile=${2:-}
            shift
            ;;
          -d|--testdata)
            testdata=$(get_cli_secret 'test data' "${2:-}")
            shift
            ;;
          -f|--force)
            force='force' ;;
          *)
            usage ;;
        esac
        shift || true
    done

    # all parameters are required
    # shellcheck disable=SC2076
    if [[ "|$cipher|$password|" =~ '||' ]]; then
        usage
    fi

    safety_checks "$force" 'false'

    # validate_cipher
    valid_ciphers=$(openssl list-cipher-commands 2>/dev/null || openssl list -cipher-commands)
    echo "$valid_ciphers" | tr -s ' ' '\n' | grep -qx "$cipher" || die "$cipher is not a valid cipher"

    # validate test file
    cd "$(git rev-parse --show-toplevel)" || die 'not in a repository'
    if [[ $testfile ]]; then
        [ -r "$testfile" ] || die "cannot open test file $testfile"
        [[ $testdata ]] || die "test data can't be empty"
        setup_encoding nosalt "$cipher" "$password"
        # shellcheck disable=SC2086
        result=$(openssl $enc_args -d -in "$testfile" 2>/dev/null || true)
        if [ "$result" != "$testdata" ]; then
            [ "$(cat "$testfile")" = "$testdata" ] || die 'data test failed'
            [ "$force" = 'force' ] || die 'test file is already decoded'
            echo 'warning: test file is already decoded'
        fi
    fi

    git config --local ivacrypt.cipher   "$cipher"
    git config --local ivacrypt.password "$password"
    get_status  # check if branch was encrypted
    upgrade_repo 'force'
    mark_branch "$branch" "$status" 'just_mark'

    force_checkout
    chmod_crypt
    git status --short
}

get_cli_secret() {
    name=$1
    arg=${2:-}
    case "$arg" in
      pass:*)
        value=${arg#pass:}
        ;;
      env:*)
        var=${arg#env:}
        value=${!var:-} || value=''
        [[ $value ]] || die "$name variable '$var' is undefined"
        ;;
      file:*)
        file=${arg#file:}
        value=$(cat "$file" || true)
        [[ $value ]] || die "cannot read $name from '$file'"
        ;;
      *)
        usage ;;
    esac
    [[ $value ]] || usage
    echo "$value"
}

safety_checks() {
    force=${1:-}
    want_configured=${2:-}

    case "$force" in
      -f|force)
        force='force' ;;
    esac

    for cmd in openssl git mktemp grep xargs; do
        command -v $cmd >/dev/null || die "required but not found: $cmd"
    done

    git_dir=$(git rev-parse --git-dir 2>/dev/null || true)
    git_dir=$(readlink -f "$git_dir" 2>/dev/null)
    [ -d "$git_dir" ] || die 'you are not in a git repository'

    configured=$(git config --get --local ivacrypt.configured 2>/dev/null || true)
    if [ "$want_configured" = 'true' ] && [ "$configured" != 'true' ] && [ "$force" != 'force' ] ; then
        die 'repository is not configured'
    fi
    if [ "$want_configured" = 'false' ] && [ "$configured" = 'true' ] && [ "$force" != 'force' ]; then
        die 'repository is already configured'
    fi

    if [ "$force" != 'force' ] && [ "$force" != 'ignore_dirty' ]; then
        # ensure the repository is clean (if it has a HEAD revision)
        head_id=$(git rev-parse --verify --quiet HEAD 2>/dev/null || true)
        is_bare=$(git rev-parse --is-bare-repository 2>/dev/null || true)
        if [[ $head_id ]] && [ "$is_bare" = 'false' ]; then
            git diff-index --quiet HEAD -- || die 'repository is dirty, commit or stash your changes'
        fi
    fi
}

upgrade_repo() {
    force=${1:-}
    safety_checks "$force" 'true'

    mkdir -p "$git_dir/ivacrypt"
    cp "$script_path" "$git_dir/ivacrypt/cryptor"
    chmod 755 "$git_dir/ivacrypt/cryptor"

    # shellcheck disable=SC2016
    executable='"$(git rev-parse --git-common-dir)"/ivacrypt/cryptor'
    git config --local filter.crypt.clean    "$executable clean %f"
    git config --local filter.crypt.smudge   "$executable smudge"
    git config --local diff.crypt.textconv   "$executable textconv"
    git config --local diff.crypt.binary     "$is_binary"
    git config --local merge.crypt.name      'merge driver for crypted files'
    git config --local merge.crypt.driver    "$executable merge-driver %O %A %B %P"
    [ -z "$merge_mode" ] || git config --local merge.crypt.recursive "$merge_mode"
    git config --local diff.crypt.cachetextconv "$cache_textconv"

    set_flags="filter.crypt.required merge.renormalize ivacrypt.configured"
    for flag in $set_flags ;do git config --local "$flag" 'true' ;done

    set_names="ls-crypt chmod-crypt branch-encrypted branch-decrypted ls-blobs"
    for name in $set_names ;do git config --local "alias.$name" "! $executable $name" ;done
    git config --local "alias.crypt" "! $executable"

    git config --local ivacrypt.version "$script_version"
    chmod 600 "$git_dir/config"
    purge_cache
    echo "setup complete"
}

teardown() {
    safety_checks 'ignore_dirty' 'true'

    for section in ivacrypt filter.crypt diff.crypt merge.crypt; do
        git config --remove-section --local "$section" 2>/dev/null || true
    done
    for flag in merge.renormalize ivacrypt.configured; do
        git config --unset --local "$flag" 2>/dev/null || true
    done

    for alias in ls-crypt chmod-crypt branch-encrypted branch-decrypted; do
        git config --unset --local "alias.$alias" || true
    done

    remove_if_empty merge
    remove_if_empty alias
    force_checkout
    echo "teardown complete"
}

usage()
{
cat <<EOF 1>&2
usage: $prog command...
commands:
  encrypt|decrypt [new_branch] [-f]
  ls-blobs [log_args...]
  ls-crypt|chmod-crypt
  branch-encrypted|branch-decrypted [branch]
  purge|status|teardown|update-secrets
  setup [-f] -c cipher -p <password> [-t test_file -d <test_data>]
  upgrade [-f]
  clean|smudge|textconv
syntax of password and test_data arguments:
  pass:<string> | env:<variable> | file:<filename>
EOF
exit 1
}

case "$action" in
  clean)
    filter_clean ;;
  smudge)
    filter_smudge ;;
  textconv)
    filter_textconv ;;
  merge-driver)
    shift
    merge_driver "$@" ;;
  ls-crypt)
    ls_crypt ;;
  chmod-crypt)
    chmod_crypt ;;
  branch-encrypted)
    mark_branch "$filename" 'encrypt' 'pushes' ;;
  branch-decrypted)
    mark_branch "$filename" 'decrypt' 'pushes' ;;
  encrypt|decrypt)
    convert_branch "$@" ;;
  purge)
    purge_cache ;;
  status)
    check_status ;;
  update-secrets)
    force_checkout ;;
  upgrade)
    upgrade_repo "$filename" ;;
  teardown)
    teardown ;;
  setup)
    shift
    setup_repo "$@" ;;
  ls-blobs)
    shift
    raw_log "$@" ;;
  on-convert-commit)
    on_convert_commit ;;
  *)
    usage ;;
esac
exit 0
