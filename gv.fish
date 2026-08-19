function gv
    if test (count $argv) -eq 0
        set -l path (grove --null | string split0)
        set -l grove_status $pipestatus[1]
        test $grove_status -eq 0
        or return $grove_status
        test -n "$path"
        and builtin cd -- $path
        return
    end

    set -l subcommand $argv[1]
    set -l rest $argv[2..]

    if contains -- --help $rest; or contains -- -h $rest; or contains -- --json $rest
        grove $argv
        return $status
    end

    switch $subcommand
        case n new
            set -l path (grove --null new $rest | string split0)
            set -l grove_status $pipestatus[1]
            test $grove_status -eq 0
            or return $grove_status
            test -n "$path"
            and builtin cd -- $path
        case cd
            set -l path (grove --null cd $rest | string split0)
            set -l grove_status $pipestatus[1]
            test $grove_status -eq 0
            or return $grove_status
            test -n "$path"
            and builtin cd -- $path
        case rm
            if contains -- --merged $rest; or contains -- --dry-run $rest; or string match -q -- '--merged=*' $rest; or string match -q -- '--dry-run=*' $rest
                grove rm $rest
                return $status
            end
            set -l path (grove --null rm $rest | string split0)
            set -l grove_status $pipestatus[1]
            test $grove_status -eq 0
            or return $grove_status
            test -n "$path"
            and builtin cd -- $path
        case l ls list
            grove list $rest
        case cfg config
            grove config $rest
        case '*'
            grove $argv
    end
end
