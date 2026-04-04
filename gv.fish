function gv
    if test (count $argv) -eq 0
        grove --help
        return
    end

    set -l subcmd $argv[1]
    set -l rest $argv[2..]

    switch $subcmd
        case cfg config
            grove config $rest
        case sh shadow
            grove shadow $rest
        case start
            grove start $rest
        case '*'
            grove $argv
    end
end
