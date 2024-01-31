# datastorer
some play with goroutines


    my workflow:
	   1) Create a header with inventory document number
	   2) Initialize product inventory snapshot from global service
	   3) Check every x seconds if the snapshot is ready
	   4) Copy/download the first 1000 records and send via channel to the function saving to the local details database
	   5) Save the data to the local details database in batches of 1000 records -> back to 4
	   6) send command to generate an Excel report
	