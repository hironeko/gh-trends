package animation

import (
	"fmt"
	"sync"
	"time"
)

// Global spinner management to prevent interference
var (
	globalSpinnerMutex sync.Mutex
	activeSpinner      *ShibaSpinner
	spinnersSuppressed bool
)

// SuppressSpinners disables the animated dog spinner until the returned
// restore function is called. This is useful when progress is rendered by a
// different UI, such as the local dashboard.
func SuppressSpinners() func() {
	globalSpinnerMutex.Lock()
	previous := spinnersSuppressed
	spinnersSuppressed = true
	globalSpinnerMutex.Unlock()

	return func() {
		globalSpinnerMutex.Lock()
		spinnersSuppressed = previous
		globalSpinnerMutex.Unlock()
	}
}

// ShibaFrames contains running animation frames
var ShibaFrames = []string{
	"🐕💨 ",
	"🐕💨💨 ",
	"🐕💨💨💨 ",
	"🐕💨 ",
}

// ASCII Art Shiba Inu frames for running animation
var DetailedShibaFrames = []string{
	// Frame 1 - Running right
	`    ∩───∩
   (  ◕   ◕  )   Fetching data...
    ＼  ω  ／    ~~~ ~~~ ~~~
     ∪─▲─∪      
    ╱       ╲     
   (  )   (  )    `,

	// Frame 2 - Mid-run, legs different
	`     ∩───∩
    (  ◕   ◕  )  Fetching data...
     ＼  ω  ／   ~~~ ~~~ ~~~
      ∪─▲─∪     
     ╱       ╲    
    (  ) (  )     `,

	// Frame 3 - Running right
	`      ∩───∩
     (  ◕   ◕  ) Fetching data...
      ＼  ω  ／  ~~~ ~~~ ~~~
       ∪─▲─∪    
      ╱       ╲   
     (  )   (  )  `,

	// Frame 4 - Mid-run
	`       ∩───∩
      (  ◕   ◕  )Fetching data...
       ＼  ω  ／ ~~~ ~~~ ~~~
        ∪─▲─∪   
       ╱       ╲  
      (  ) (  )   `,
}

// ShibaSpinner creates an animated loading indicator with a running shiba inu
type ShibaSpinner struct {
	frames   []string
	delay    time.Duration
	stopChan chan bool
	message  string
}

// NewShibaSpinner creates a new shiba spinner with custom message
func NewShibaSpinner(message string, useDetailed bool) *ShibaSpinner {
	frames := ShibaFrames
	if useDetailed {
		frames = DetailedShibaFrames
	}

	return &ShibaSpinner{
		frames:   frames,
		delay:    300 * time.Millisecond,
		stopChan: make(chan bool),
		message:  message,
	}
}

// Start begins the animation in a separate goroutine
func (s *ShibaSpinner) Start() {
	globalSpinnerMutex.Lock()
	if spinnersSuppressed {
		globalSpinnerMutex.Unlock()
		return
	}
	if activeSpinner != nil {
		activeSpinner.Stop()
	}
	activeSpinner = s
	globalSpinnerMutex.Unlock()

	go func() {
		frameIndex := 0

		// Hide cursor
		fmt.Print("\033[?25l")

		for {
			select {
			case <-s.stopChan:
				// Clear line and show cursor
				fmt.Print("\033[2K\r\033[?25h")
				globalSpinnerMutex.Lock()
				if activeSpinner == s {
					activeSpinner = nil
				}
				globalSpinnerMutex.Unlock()
				return
			default:
				// Simple line replacement for all cases
				fmt.Printf("\033[2K\r%s%s", s.frames[frameIndex], s.message)

				frameIndex = (frameIndex + 1) % len(s.frames)
				time.Sleep(s.delay)
			}
		}
	}()
}

// Stop ends the animation
func (s *ShibaSpinner) Stop() {
	select {
	case s.stopChan <- true:
	default:
		// Non-blocking send to avoid deadlock
	}
}

// UpdateMessage changes the loading message
func (s *ShibaSpinner) UpdateMessage(message string) {
	s.message = message
}

// Simple spinner without animation for CI environments
func ShowSimpleProgress(message string) {
	fmt.Printf("🔄 %s\n", message)
}
