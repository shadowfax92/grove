function __gv_flag_enabled
    set -l name $argv[1]
    set -l enabled false
    for argument in $argv[2..]
        switch $argument
            case $name "$name=1" "$name=t" "$name=T" "$name=true" "$name=True" "$name=TRUE"
                set enabled true
            case "$name=0" "$name=f" "$name=F" "$name=false" "$name=False" "$name=FALSE"
                set enabled false
        end
    end
    test "$enabled" = true
end

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

    if contains -- --help $rest; or contains -- -h $rest; or __gv_flag_enabled --json $rest
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
            if __gv_flag_enabled --merged $rest; or __gv_flag_enabled --dry-run $rest
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
